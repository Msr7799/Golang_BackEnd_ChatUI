# Chat UI Go Backend
![alt text](<Chat ui-1.png>)
باك إند آمن لتطبيق Android Chat UI. التطبيق يرسل Firebase ID token إلى هذا السيرفر، والسيرفر وحده يضيف `HF_API_KEY` عند الاتصال بـ Hugging Face Router.

## Endpoints

- `GET /healthz` عام بدون توكن. يوجد alias عام أيضًا: `GET /healthz/` و`GET /v1/healthz`.
  - في Cloud Run الحالي تم التحقق من `GET /healthz/` و`GET /v1/healthz` بنجاح.
- `GET /v1/models` يحتاج `Authorization: Bearer <Firebase ID token>`.
- `POST /v1/chat` يحتاج Firebase token ويرجع JSON من Hugging Face.
- `POST /v1/chat/stream` يحتاج Firebase token ويمرر SSE stream مع flush لكل chunk.
- `POST /v1/chat/with-file` يحتاج Firebase token ويستقبل PDF عبر `multipart/form-data` ثم يستخرج النص في السيرفر.
- `GET|POST /v1/google/*` يمرر Google AI Studio مع مفتاح السيرفر.
- `POST /v1/mcp/tavily` يمرر Tavily MCP مع مفتاح السيرفر عبر `Authorization` header وليس query string.
- `POST /v1/cloudinary/upload` يرفع الصور والفيديوهات فقط إلى Cloudinary من السيرفر.

## Environment

انسخ `.env.example` إلى `.env` محليًا وضع القيم:

```bash
HF_API_KEY=hf_...
FIREBASE_PROJECT_ID=your-firebase-project-id
GOOGLE_APPLICATION_CREDENTIALS=/absolute/path/service-account.json
PORT=8080
```

الأسرار المطلوبة فعليًا:

- `HF_API_KEY`: مفتاح Hugging Face، يبقى في السيرفر فقط.
- `GOOGLE_STUDIO_API_KEY`: مفتاح Google AI Studio/Gemini.
- `TAVILY_API_KEY`: مفتاح Tavily المستخدم عبر MCP proxy.
- `CLOUDINARY_CLOUD_NAME`, `CLOUDINARY_API_KEY`, `CLOUDINARY_API_SECRET`: أسرار الرفع إلى Cloudinary من السيرفر.
- `MAX_PROMPT_CHARS`: حد طول المحادثة المرسلة للنموذج. القيمة `0` تعني بدون حد من الباك إند.
- `MAX_UPLOAD_MB`: حد الرفع. الافتراضي `20` ميجابايت، ويفضل عدم رفعه إلا عند الحاجة.
- `MAX_PDF_UPLOAD_MB`: حد PDF في `/v1/chat/with-file`. الافتراضي `20`.
- `MAX_PDF_TEXT_CHARS`: أقصى عدد أحرف مستخرجة من PDF يتم إرسالها للنموذج. الافتراضي `60000`.
- `ALLOWED_ORIGINS`: دومينات الويب المسموحة فقط. للأندرويد لا تحتاج `*` في الإنتاج.
- `FIREBASE_PROJECT_ID`: رقم/اسم مشروع Firebase.
- `GOOGLE_APPLICATION_CREDENTIALS`: ملف service account محليًا فقط. في Cloud Run استخدم Service Account بدل ملف.

## Local Run

```bash
cd GO_BACKEND
cp .env.example .env
set -a
source .env
set +a
go run ./cmd/server
```

اختبار الصحة:

```bash
curl http://localhost:8080/healthz
```

مثال طلب شات:

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3-235B-A22B-Instruct-2507",
    "messages": [{"role": "user", "content": "مرحبا"}],
    "temperature": 0.7,
    "max_tokens": 1024
  }'
```

مثال stream:

```bash
curl -N -X POST http://localhost:8080/v1/chat/stream \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3-235B-A22B-Instruct-2507",
    "messages": [{"role": "user", "content": "اكتب فقرة قصيرة"}]
  }'
```

مثال PDF multipart بدون Base64:

```bash
curl -X POST http://localhost:8080/v1/chat/with-file \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  -F "model=Qwen/Qwen3-235B-A22B-Instruct-2507" \
  -F "message=حلل هذا الملف" \
  -F "file=@/path/to/file.pdf;type=application/pdf"
```

## Firestore Usage

يسجل الاستخدام في:

```text
users/{uid}/usage/summary
```

الحقول:

- `totalRequests`
- `lastRequestAt`
- `dailyCount`
- `dailyDate`
- `modelUsage`

استخدمت `summary` لأن Firestore يحتاج document ID بعد collection، بينما `users/{uid}/usage` وحدها تكون collection path.

## Docker

```bash
cd GO_BACKEND
docker build -t chat-ui-go-backend .
docker run --rm -p 8080:8080 --env-file .env chat-ui-go-backend
```

## Deploy على Render

1. أنشئ Web Service من الريبو.
2. Root Directory: `GO_BACKEND`.
3. Runtime: Docker.
4. أضف env vars:
   - `HF_API_KEY`
   - `GOOGLE_STUDIO_API_KEY`
   - `TAVILY_API_KEY`
   - `CLOUDINARY_CLOUD_NAME`
   - `CLOUDINARY_API_KEY`
   - `CLOUDINARY_API_SECRET`
   - `FIREBASE_PROJECT_ID`
   - `ALLOWED_ORIGINS`
   - `MAX_PROMPT_CHARS`
   - `RATE_LIMIT_PER_MINUTE`
5. أضف service account عبر Secret File أو استخدم طريقة Render المتاحة للـGoogle credentials، ثم عيّن `GOOGLE_APPLICATION_CREDENTIALS`.

## Deploy على Cloud Run

```bash
cd GO_BACKEND
gcloud run deploy chat-ui-go-backend \
  --source . \
  --region us-central1 \
  --allow-unauthenticated \
  --set-env-vars FIREBASE_PROJECT_ID=your-project-id,ALLOWED_ORIGINS='https://your-web-domain.example',MAX_UPLOAD_MB=20 \
  --set-secrets HF_API_KEY=HF_API_KEY:latest,GOOGLE_STUDIO_API_KEY=GOOGLE_STUDIO_API_KEY:latest,TAVILY_API_KEY=TAVILY_API_KEY:latest,CLOUDINARY_API_SECRET=CLOUDINARY_API_SECRET:latest
```

الأفضل أن تعطي Cloud Run service account صلاحيات Firebase Auth/Firestore المناسبة، ولا ترفع service account JSON داخل الصورة.

ملاحظات أمان:

- لا تضع `ALLOWED_ORIGINS=*` في الإنتاج إلا لو كان عندك سبب مؤقت واضح.
- مسار Tavily يزيل `tavilyApiKey` من URL ويضيف المفتاح في `Authorization: Bearer ...`.
- مسار Cloudinary يمنع `raw` ويقبل `image/*` و`video/*` فقط مع تحقق MIME.

## Android Integration

تطبيق Android لا يرسل إلى Hugging Face مباشرة. اجعل إعداد Hugging Face base URL يشير إلى:

```text
http://10.0.2.2:8080/v1
```

على جهاز حقيقي استخدم رابط السيرفر المنشور، مثل:

```text
https://your-service.onrender.com/v1
```

العميل يجلب Firebase ID token من Firebase Auth ثم يرسل:

```text
Authorization: Bearer <Firebase ID token>
```

بهذا لا يوجد `HF_API_KEY` داخل APK أو داخل إعدادات تطبيق Android.

كذلك اجعل الخدمات الثانية تشير للباك إند:

```properties
GOOGLE_AI_STUDIO_BASE_URL=https://your-backend.example.com/v1/google
TAVILY_MCP_URL=https://your-backend.example.com/v1/mcp/tavily
CLOUDINARY_API_KEY=
CLOUDINARY_API_SECRET=
```

العميل يرسل Firebase token فقط، والباك إند يضيف مفاتيح Google/Tavily/Cloudinary.
