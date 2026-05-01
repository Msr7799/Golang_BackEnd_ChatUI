package handlers

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"chat-ui-go-backend/internal/types"
)

type CloudinaryHandler struct {
	cloudName      string
	apiKey         string
	apiSecret      string
	defaultFolder  string
	maxUploadBytes int64
	client         *http.Client
}

func NewCloudinaryHandler(cloudName, apiKey, apiSecret, defaultFolder string, maxUploadBytes int64, requestTimeout time.Duration) *CloudinaryHandler {
	return &CloudinaryHandler{
		cloudName:      cloudName,
		apiKey:         apiKey,
		apiSecret:      apiSecret,
		defaultFolder:  defaultFolder,
		maxUploadBytes: maxUploadBytes,
		client:         &http.Client{Timeout: requestTimeout},
	}
}

func (h *CloudinaryHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if h.cloudName == "" || h.apiKey == "" || h.apiSecret == "" {
		types.WriteError(w, r, http.StatusServiceUnavailable, "cloudinary is not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	if err := r.ParseMultipartForm(h.maxUploadBytes); err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "invalid multipart upload")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	// حماية مهمة: نسمح فقط بالصور والفيديو ونمنع raw حتى لا يتحول الرفع لمخزن ملفات عام.
	resourceType, uploadReader, err := validateCloudinaryUpload(file, header, r.FormValue("resource_type"))
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	folder := strings.TrimSpace(r.FormValue("folder"))
	if folder == "" {
		folder = h.defaultFolder
	}
	tags := strings.TrimSpace(r.FormValue("tags"))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		types.WriteError(w, r, http.StatusInternalServerError, "upload preparation failed")
		return
	}
	if _, err := io.Copy(fileWriter, uploadReader); err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "failed to read upload")
		return
	}

	params := map[string]string{
		"timestamp": timestamp,
	}
	if folder != "" {
		params["folder"] = folder
	}
	if tags != "" {
		params["tags"] = tags
	}
	signature := signCloudinary(params, h.apiSecret)

	_ = writer.WriteField("api_key", h.apiKey)
	_ = writer.WriteField("timestamp", timestamp)
	_ = writer.WriteField("signature", signature)
	if folder != "" {
		_ = writer.WriteField("folder", folder)
	}
	if tags != "" {
		_ = writer.WriteField("tags", tags)
	}
	_ = writer.Close()

	endpoint := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/%s/upload", h.cloudName, resourceType)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, &body)
	if err != nil {
		types.WriteError(w, r, http.StatusInternalServerError, "upload request failed")
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := h.client.Do(req)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "cloudinary upload failed")
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "cloudinary response failed")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		types.WriteError(w, r, http.StatusBadGateway, "cloudinary upload was rejected")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBody)
}

func validateCloudinaryUpload(file multipart.File, header *multipart.FileHeader, requestedResourceType string) (string, io.Reader, error) {
	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && err != io.EOF {
		return "", nil, fmt.Errorf("failed to read upload")
	}
	sniff = sniff[:n]

	detected := http.DetectContentType(sniff)
	declared := strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	extensionType := strings.ToLower(mime.TypeByExtension(strings.ToLower(filepath.Ext(header.Filename))))

	fileKind := detectAllowedCloudinaryKind(detected, declared, extensionType)
	if fileKind == "" {
		return "", nil, fmt.Errorf("unsupported file type")
	}

	requested := strings.ToLower(strings.TrimSpace(requestedResourceType))
	switch requested {
	case "", "auto":
		// Cloudinary endpoint يحتاج resource type واضح؛ نستنتجه من MIME الموثوق.
	case "image", "video":
		if requested != fileKind {
			return "", nil, fmt.Errorf("resource_type does not match file type")
		}
	default:
		return "", nil, fmt.Errorf("unsupported resource_type")
	}

	return fileKind, io.MultiReader(bytes.NewReader(sniff), file), nil
}

func detectAllowedCloudinaryKind(detected, declared, extensionType string) string {
	detectedMedia := cleanMediaType(detected)
	declaredMedia := cleanMediaType(declared)
	extensionMedia := cleanMediaType(extensionType)

	if strings.HasPrefix(detectedMedia, "image/") {
		return "image"
	}
	if strings.HasPrefix(detectedMedia, "video/") {
		return "video"
	}

	// بعض الفيديوهات قد تظهر كـ octet-stream عند sniffing؛ نقبلها فقط إذا اتفق MIME المعلن مع الامتداد.
	if strings.HasPrefix(declaredMedia, "video/") && strings.HasPrefix(extensionMedia, "video/") {
		return "video"
	}
	return ""
}

func cleanMediaType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(contentType))
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func signCloudinary(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "&") + secret))
	return hex.EncodeToString(sum[:])
}
