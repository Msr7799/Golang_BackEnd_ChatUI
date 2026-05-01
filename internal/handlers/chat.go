package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"chat-ui-go-backend/internal/auth"
	"chat-ui-go-backend/internal/hf"
	"chat-ui-go-backend/internal/store"
	"chat-ui-go-backend/internal/types"
)

type ChatHandler struct {
	hfClient          *hf.Client
	store             *store.FirestoreStore
	maxPromptChars    int
	maxPDFUploadBytes int64
	maxPDFTextChars   int
	requestTimeout    time.Duration
	streamTimeout     time.Duration
}

func NewChatHandler(
	hfClient *hf.Client,
	store *store.FirestoreStore,
	maxPromptChars int,
	maxPDFUploadBytes int64,
	maxPDFTextChars int,
	requestTimeout time.Duration,
	streamTimeout time.Duration,
) *ChatHandler {
	return &ChatHandler{
		hfClient:          hfClient,
		store:             store,
		maxPromptChars:    maxPromptChars,
		maxPDFUploadBytes: maxPDFUploadBytes,
		maxPDFTextChars:   maxPDFTextChars,
		requestTimeout:    requestTimeout,
		streamTimeout:     streamTimeout,
	}
}

func (h *ChatHandler) Chat(w http.ResponseWriter, r *http.Request) {
	chatRequest, err := h.decodeAndValidate(r)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		types.WriteError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	status, body, err := h.hfClient.Chat(ctx, chatRequest)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "upstream chat request failed")
		return
	}
	if status < 200 || status >= 300 {
		types.WriteError(w, r, http.StatusBadGateway, upstreamRejectMessage("upstream chat request was rejected", status, body))
		return
	}

	// نسجل الاستخدام بعد نجاح الطلب فقط، بدون تخزين محتوى المحادثة أو مفاتيح API.
	_ = h.store.IncrementUsage(r.Context(), user.UID, chatRequest.Model)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *ChatHandler) ChatWithFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		types.WriteError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxPDFUploadBytes+(1<<20))

	upload, err := h.readPDFMultipart(r)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	defer func() {
		if upload.tempPath != "" {
			_ = os.Remove(upload.tempPath)
		}
	}()

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	pdfText, err := extractPDFText(ctx, upload.tempPath)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "pdf text extraction failed")
		return
	}
	pdfText = strings.TrimSpace(pdfText)
	if pdfText == "" {
		types.WriteError(w, r, http.StatusBadRequest, "pdf has no extractable text")
		return
	}

	originalChars := utf8.RuneCountInString(pdfText)
	pdfText, truncated := truncateRunes(pdfText, h.maxPDFTextChars)

	log.Printf(
		"chat pdf analysis: uid=%s filename=%q mime=%q bytes=%d extracted_chars=%d sent_chars=%d model=%q",
		user.UID,
		safeLogValue(upload.filename),
		safeLogValue(upload.mimeType),
		upload.size,
		originalChars,
		utf8.RuneCountInString(pdfText),
		safeLogValue(upload.model),
	)

	finalPrompt := buildPDFAnalysisPrompt(upload.message, upload.filename, pdfText, truncated, originalChars)
	chatRequest := types.ChatRequest{
		Model: upload.model,
		Messages: []types.ChatMessage{
			{
				Role:    "system",
				Content: rawJSONString("You analyze extracted document text. Answer the user using only the provided document text when the question is about the attachment, and clearly say if the extracted text is insufficient."),
			},
			{
				Role:    "user",
				Content: rawJSONString(finalPrompt),
			},
		},
	}

	status, body, err := h.hfClient.Chat(ctx, chatRequest)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "upstream file analysis request failed")
		return
	}
	if status < 200 || status >= 300 {
		types.WriteError(w, r, http.StatusBadGateway, upstreamRejectMessage("upstream file analysis request was rejected", status, body))
		return
	}

	// الأمان: نخزن عداد الاستخدام فقط، ولا نخزن نص PDF أو محتوى المحادثة في Firestore.
	_ = h.store.IncrementUsage(r.Context(), user.UID, chatRequest.Model)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *ChatHandler) Stream(w http.ResponseWriter, r *http.Request) {
	chatRequest, err := h.decodeAndValidate(r)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		types.WriteError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		types.WriteError(w, r, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.streamTimeout)
	defer cancel()

	resp, err := h.hfClient.Stream(ctx, chatRequest)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "upstream stream request failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		types.WriteError(w, r, http.StatusBadGateway, upstreamRejectMessage("upstream stream request was rejected", resp.StatusCode, body))
		return
	}

	_ = h.store.IncrementUsage(r.Context(), user.UID, chatRequest.Model)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	buffer := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr != io.EOF {
				return
			}
			return
		}
	}
}

func (h *ChatHandler) decodeAndValidate(r *http.Request) (types.ChatRequest, error) {
	var chatRequest types.ChatRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&chatRequest); err != nil {
		return chatRequest, errors.New("invalid json body")
	}

	chatRequest.Model = strings.TrimSpace(chatRequest.Model)
	if chatRequest.Model == "" {
		return chatRequest, errors.New("model is required")
	}
	if len(chatRequest.Messages) == 0 {
		return chatRequest, errors.New("messages are required")
	}

	totalChars := 0
	for _, message := range chatRequest.Messages {
		role := strings.TrimSpace(message.Role)
		if role != "user" && role != "assistant" && role != "system" {
			return chatRequest, errors.New("invalid message role")
		}
		if len(message.Content) == 0 || string(message.Content) == "null" {
			return chatRequest, errors.New("message content is required")
		}

		totalChars += rawContentTextLength(message.Content)
		if h.maxPromptChars > 0 && totalChars > h.maxPromptChars {
			return chatRequest, errors.New("prompt is too long")
		}
	}

	if chatRequest.Temperature != nil && (*chatRequest.Temperature < 0 || *chatRequest.Temperature > 2) {
		return chatRequest, errors.New("temperature must be between 0 and 2")
	}
	if chatRequest.MaxTokens != nil && (*chatRequest.MaxTokens < 1 || *chatRequest.MaxTokens > 8192) {
		return chatRequest, errors.New("max_tokens must be between 1 and 8192")
	}

	return chatRequest, nil
}

func upstreamRejectMessage(prefix string, status int, body []byte) string {
	cleanBody := strings.TrimSpace(string(body))
	if cleanBody == "" {
		return fmt.Sprintf("%s: HTTP %d", prefix, status)
	}
	if len(cleanBody) > 500 {
		cleanBody = cleanBody[:500] + "..."
	}
	return fmt.Sprintf("%s: HTTP %d: %s", prefix, status, cleanBody)
}

func rawContentTextLength(raw json.RawMessage) int {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return utf8.RuneCount(raw)
	}
	return valueTextLength(decoded)
}

func valueTextLength(value any) int {
	switch v := value.(type) {
	case string:
		return utf8.RuneCountInString(v)
	case []any:
		total := 0
		for _, item := range v {
			total += valueTextLength(item)
		}
		return total
	case map[string]any:
		total := 0
		for key, item := range v {
			if key == "image_url" || key == "url" {
				continue
			}
			total += valueTextLength(item)
		}
		return total
	default:
		return 0
	}
}

type pdfUpload struct {
	message  string
	model    string
	filename string
	mimeType string
	tempPath string
	size     int64
}

func (h *ChatHandler) readPDFMultipart(r *http.Request) (pdfUpload, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return pdfUpload{}, errors.New("invalid multipart upload")
	}

	var upload pdfUpload
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return upload, errors.New("invalid multipart upload")
		}

		switch part.FormName() {
		case "message":
			value, err := readSmallMultipartField(part, "message")
			if err != nil {
				return upload, err
			}
			upload.message = strings.TrimSpace(value)
		case "model":
			value, err := readSmallMultipartField(part, "model")
			if err != nil {
				return upload, err
			}
			upload.model = strings.TrimSpace(value)
		case "file":
			if upload.tempPath != "" {
				return upload, errors.New("only one file is supported")
			}
			if err := h.storePDFPart(part, &upload); err != nil {
				return upload, err
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 4<<10))
		}
	}

	if upload.message == "" {
		return upload, errors.New("message is required")
	}
	if upload.model == "" {
		return upload, errors.New("model is required")
	}
	if upload.tempPath == "" {
		return upload, errors.New("file is required")
	}
	if !looksLikePDFMeta(upload.filename, upload.mimeType) {
		return upload, errors.New("unsupported file type: only PDF files are supported")
	}
	if ok := hasPDFMagic(upload.tempPath); !ok {
		return upload, errors.New("unsupported file type: invalid PDF")
	}

	return upload, nil
}

func (h *ChatHandler) storePDFPart(part *multipart.Part, upload *pdfUpload) error {
	filename := strings.TrimSpace(filepath.Base(part.FileName()))
	if filename == "" || filename == "." {
		return errors.New("file name is required")
	}

	mimeType := strings.TrimSpace(part.Header.Get("Content-Type"))
	if !looksLikePDFMeta(filename, mimeType) {
		return errors.New("unsupported file type: only PDF files are supported")
	}

	tmp, err := os.CreateTemp("", "chatui-pdf-*.pdf")
	if err != nil {
		return errors.New("failed to prepare upload")
	}
	defer tmp.Close()

	written, err := copyLimited(tmp, part, h.maxPDFUploadBytes)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}

	upload.filename = filename
	upload.mimeType = mimeType
	upload.tempPath = tmp.Name()
	upload.size = written
	return nil
}

func readSmallMultipartField(part *multipart.Part, fieldName string) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, 64<<10))
	if err != nil {
		return "", fmt.Errorf("%s is invalid", fieldName)
	}
	return string(data), nil
}

func copyLimited(dst *os.File, src io.Reader, maxBytes int64) (int64, error) {
	limited := io.LimitReader(src, maxBytes+1)
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, errors.New("failed to read file")
	}
	if written > maxBytes {
		return written, errors.New("file is too large")
	}
	return written, nil
}

func looksLikePDFMeta(filename, mimeType string) bool {
	lowerName := strings.ToLower(filename)
	lowerMime := strings.ToLower(mimeType)
	return strings.HasSuffix(lowerName, ".pdf") || strings.Contains(lowerMime, "pdf")
}

func hasPDFMagic(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	header := make([]byte, 5)
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	return n == 5 && string(header) == "%PDF-"
}

func extractPDFText(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext failed: %w", err)
	}
	return string(output), nil
}

func truncateRunes(value string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value, false
	}

	var builder strings.Builder
	builder.Grow(len(value))
	count := 0
	for _, r := range value {
		if count >= maxRunes {
			break
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String(), true
}

func buildPDFAnalysisPrompt(userMessage, filename, pdfText string, truncated bool, originalChars int) string {
	var builder strings.Builder
	builder.WriteString("User request:\n")
	builder.WriteString(userMessage)
	builder.WriteString("\n\nAttached PDF filename:\n")
	builder.WriteString(filename)
	if truncated {
		builder.WriteString("\n\nNote: The extracted PDF text was truncated for model context. Original extracted characters: ")
		builder.WriteString(strconv.Itoa(originalChars))
	}
	builder.WriteString("\n\nExtracted PDF text:\n")
	builder.WriteString(pdfText)
	return builder.String()
}

func rawJSONString(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func safeLogValue(value string) string {
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	return replacer.Replace(value)
}
