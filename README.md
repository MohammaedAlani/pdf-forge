# PDF Forge 🛠️

<div align="center">

![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=for-the-badge&logo=go)
![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=for-the-badge&logo=docker)
![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)

**🚀 Enterprise-Grade PDF Conversion Microservice**

*Convert anything to PDF • Templates • Manipulation • Security • Webhooks • S3 Storage*

[Features](#-features) •
[Quick Start](#-quick-start) •
[API Reference](#-api-reference) •
[Templates](#-templates) •
[Security](#-security) •
[Deployment](#-deployment)

</div>

---

## ✨ Features

### 🔄 Multi-Format Conversion
| Input | Output | Description |
|-------|--------|-------------|
| HTML | PDF | Raw HTML or Base64, bounded request and output sizes |
| URL | PDF | Screenshot any webpage |
| Images | PDF | PNG, JPG, GIF, WebP - single or batch |
| Markdown | PDF | With syntax highlighting |
| Tables | PDF | CSV/JSON data to formatted tables |

### 📄 PDF Manipulation
| Operation | Description |
|-----------|-------------|
| **Split** | Split into individual pages or chunks |
| **Merge** | Combine multiple PDFs |
| **Extract** | Extract specific pages |
| **Rotate** | Rotate pages 90°/180°/270° |
| **Compress** | Reduce file size (up to 90%) |
| **Remove** | Delete specific pages |
| **Reorder** | Change page order |
| **To Images** | Convert pages to JPG/PNG |
| **Info** | Get metadata and page count |

### 📝 Built-in Templates
- 📃 **Invoice** - Professional invoices with line items
- 🧾 **Receipt** - Point of sale receipts
- 🏆 **Certificate** - Awards and completion certificates
- 📊 **Report** - Business reports with metrics
- 📜 **Contract** - Legal contracts with signatures
- 🎨 **Custom** - Your own HTML templates with variables

### 🔒 Security Features
- **Password Protection** - User password to open PDFs
- **Owner Password** - Control editing/printing permissions
- **256-bit AES Encryption** - Enterprise-grade security
- **Permission Control** - Printing, copying, modification

### ☁️ Enterprise Features
| Feature | Description |
|---------|-------------|
| **Webhooks** | Async processing with callbacks |
| **S3 Storage** | Upload directly to S3/MinIO/DigitalOcean |
| **Batch Processing** | Convert multiple files at once |
| **Rate Limiting** | Protect against abuse |
| **API Key Auth** | Secure your endpoints |
| **Prometheus Metrics** | Production monitoring |
| **OpenAPI Spec** | Full API documentation |

---

## 🚀 Quick Start

### Using Docker

```bash
  # with configuration
docker run -p 8080:8080 \
  -e API_KEY="your-secret-key" \
  -e MAX_WORKERS=8 \
  ghcr.io/yourusername/pdf-forge:latest
```

### Test It

```bash
  # Health check
curl http://localhost:8080/health

# Simple HTML to PDF
curl -X POST http://localhost:8080/html \
  -H "Content-Type: application/json" \
  -d '{"html": "<h1>Hello World</h1>"}' \
  -o hello.pdf

  # Password protected PDF
curl -X POST http://localhost:8080/convert \
  -H "Content-Type: application/json" \
  -d '{
    "type": "html",
    "html": "<h1>Confidential</h1>",
    "options": {
      "security": {
        "user_password": "secret123",
        "encryption_bits": 256
      }
    }
  }' -o protected.pdf
```

---

## 📚 API Reference

### Conversion Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/convert` | Universal conversion |
| POST | `/html` | HTML to PDF |
| POST | `/url` | URL to PDF |
| POST | `/image` | Image(s) to PDF |
| POST | `/markdown` | Markdown to PDF |
| POST | `/table` | Table data to PDF |

### Template Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/template` | Generate from template |

### Manipulation Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/merge` | Merge PDFs |
| POST | `/manipulate` | Split/rotate/compress/etc. |

### Enterprise Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/async` | Async with webhook |
| POST | `/batch` | Batch processing |
| GET | `/health` | Health check |
| GET | `/metrics` | Prometheus metrics |

---

## 📝 Templates

### Invoice Example

```bash
  curl -X POST http://localhost:8080/template \
  -H "Content-Type: application/json" \
  -d '{
    "template": "invoice",
    "data": {
      "company_name": "ACME Corp",
      "company_address": "123 Business St, City",
      "invoice_number": "INV-001",
      "due_date": "2024-12-31",
      "client_name": "John Doe",
      "client_email": "john@example.com",
      "items": [
        {"description": "Web Development", "quantity": 10, "unit_price": 150, "amount": 1500},
        {"description": "Design Work", "quantity": 5, "unit_price": 100, "amount": 500}
      ],
      "subtotal": 2000,
      "tax_rate": 10,
      "tax": 200,
      "total": 2200,
      "currency": "$"
    }
  }' -o invoice.pdf
```

### Certificate Example

```bash
  curl -X POST http://localhost:8080/template \
  -H "Content-Type: application/json" \
  -d '{
    "template": "certificate",
    "data": {
      "title": "Certificate of Achievement",
      "recipient_name": "Jane Smith",
      "description": "For outstanding performance in the 2024 Sales Excellence Program",
      "date": "December 1, 2024",
      "signatures": [
        {"name": "John CEO", "title": "Chief Executive Officer"}
      ]
    },
    "options": {
      "orientation": "landscape"
    }
  }' -o certificate.pdf
```

### Custom Template

```bash
  curl -X POST http://localhost:8080/template \
  -H "Content-Type: application/json" \
  -d '{
    "template": "custom",
    "custom_html": "<html><body><h1>Hello {{.name}}!</h1><p>Order #{{.order_id}}</p></body></html>",
    "data": {
      "name": "John",
      "order_id": "12345"
    }
  }' -o custom.pdf
```

---

## 🔧 PDF Manipulation

### Split PDF

```bash
# Split into individual pages
  curl -X POST http://localhost:8080/manipulate \
  -H "Content-Type: application/json" \
  -d "{
    \"operation\": \"split\",
    \"pdf\": \"$(base64 -w0 document.pdf)\",
    \"options\": {\"split_type\": \"all\"}
  }"
```

### Compress PDF

```bash
  curl -X POST http://localhost:8080/manipulate \
  -d "{
    \"operation\": \"compress\",
    \"pdf\": \"$(base64 -w0 large.pdf)\",
    \"options\": {\"compression_level\": \"ebook\"}
  }"
```

**Compression Levels:** `screen` (72dpi) | `ebook` (150dpi) | `printer` (300dpi) | `prepress`

### Rotate Pages

```bash
  curl -X POST http://localhost:8080/manipulate \
  -d "{
    \"operation\": \"rotate\",
    \"pdf\": \"...\",
    \"options\": {\"rotation\": 90, \"pages\": \"1-3,5\"}
  }"
```

### PDF to Images

```bash
  curl -X POST http://localhost:8080/manipulate \
  -d "{
    \"operation\": \"to_images\",
    \"pdf\": \"...\",
    \"options\": {\"image_format\": \"png\", \"dpi\": 300}
  }"
```

---

## ☁️ Async & Webhooks

Process in background with webhook callback:

```bash
  curl -X POST http://localhost:8080/async \
  -H "Content-Type: application/json" \
  -d '{
    "request": {
      "type": "html",
      "html": "<h1>Large Report</h1>..."
    },
    "webhook": {
      "url": "https://your-server.com/webhook",
      "secret": "your-hmac-secret",
      "include_pdf": true
    },
    "storage": {
      "provider": "s3",
      "bucket": "my-bucket",
      "path": "reports/",
      "region": "us-east-1",
      "access_key_id": "AKIA...",
      "secret_access_key": "..."
    }
  }'
```

### Webhook Payload

```json
{
  "event": "conversion.completed",
  "request_id": "abc-123",
  "success": true,
  "file_size": 125000,
  "duration_ms": 1500,
  "pdf": "base64...",
  "storage": {"provider": "s3", "url": "https://..."}
}
```

---

## 📦 Batch Processing

```bash
  curl -X POST http://localhost:8080/batch \
  -H "Content-Type: application/json" \
  -d '{
    "requests": [
      {"type": "html", "html": "<h1>Doc 1</h1>"},
      {"type": "html", "html": "<h1>Doc 2</h1>"},
      {"type": "url", "url": "https://example.com"}
    ],
    "merge": true
  }'
```

---

## 🔒 Security

### Password Protection

```json
{
  "options": {
    "security": {
      "user_password": "to-open",
      "owner_password": "full-access",
      "allow_printing": true,
      "allow_copying": false,
      "encryption_bits": 256
    }
  }
}
```

---

## ⚙️ Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `ADDRESS` | `:8080` | Listen address |
| `API_KEY` | - | API key for auth |
| `MAX_WORKERS` | `4` | Concurrent workers |
| `CHROME_USE_DEV_SHM` | `false` | Use /dev/shm for Chrome; Compose enables it with a 256 MiB mount |
| `MAX_BODY_SIZE` | `33554432` | Max request bytes (32 MiB) |
| `MAX_INFLIGHT_REQUESTS` | `16` | Admitted requests, including background jobs through delivery |
| `MAX_INFLIGHT_BYTES` | `134217728` | Reserved input bytes (128 MiB); unknown-length requests reserve MAX_BODY_SIZE |
| `PROCESSING_WORKERS` | `2` | Concurrent post-processing, merge, and manipulation jobs |
| `REQUEST_TIMEOUT` | `120` | Synchronous request deadline in seconds |
| `RATE_LIMIT` | `0` | Requests/min (0=off) |

---

## 📊 Monitoring

```bash
  # Health
curl http://localhost:8080/health

  # Prometheus metrics
curl http://localhost:8080/metrics
```

---

## 🚢 Deployment

### Docker Compose

```bash
  docker-compose up -d
```

### Build from Source

```bash
  git clone https://github.com/yourusername/pdf-forge.git
  cd pdf-forge
  make build
  ./bin/pdf-forge
```

---

## 📖 API Documentation

OpenAPI 3.0 spec available at `api/openapi.yaml`

---

## 📄 License

MIT License - see [LICENSE](LICENSE)

---

<div align="center">

**Made with ❤️ for developers who need reliable PDF generation**

⭐ **Star this repo if you find it useful!**

</div>

## Performance and resource limits

Chrome stays running between jobs. Each job uses an isolated browser context,
so cookies and browser storage are not shared. Browser crashes fail active jobs;
the next conversion attempts to start a new browser. Rendering waits for page
load, fonts, and image decoding. For pages populated asynchronously, provide
`options.wait_for_expression`, for example `"window.reportReady === true"`.
The predicate has a 30-second limit within the overall render deadline.

Admission runs before body decoding. Overloaded requests receive HTTP 503 with
`Retry-After: 1`; oversized bodies receive HTTP 413. The input byte budget is
not a total memory limit: Chrome, PDF libraries, decoded inputs, and outputs
consume additional memory. Existing deployments explicitly setting a larger
`MAX_BODY_SIZE` must also configure a suitable `MAX_INFLIGHT_BYTES` budget.

Outputs are limited to 32 MiB of decoded data per document or batch. Batches
and merges accept at most 32 inputs, and batches process two items concurrently
while retaining response order. Split/rasterization inputs are limited to 100
pages; rasterization accepts at most 300 DPI and caps the longest image edge
at 4096 pixels. Outputs are generated one page at a time. PDF subprocesses have
a 90-second processing deadline and a best-effort 64 MiB scratch-directory guard.
Pure-Go PDF library calls check cancellation between stages; they cannot be
interrupted in the middle of a library call. Container limits remain essential.

Async jobs have a five-minute deadline and retain admission reservations until
completion. Four webhook deliveries can run concurrently, independently of the
rendering and post-processing slots. Slow callbacks can still exhaust the shared
admission budget, deliberately bounding retained work. Jobs and callbacks remain
in memory; shutdown cancels them and they are not durable across restarts.

Rate limits use the direct peer IP (source ports are ignored). Forwarded headers
are not trusted; behind a reverse proxy the limit therefore applies to the proxy
IP. Health and metrics bypass rate and resource admission limits.

Both Compose files reserve 256 MiB for Chrome shared memory and enable
`CHROME_USE_DEV_SHM=true`. Other deployments retain Chrome’s temporary-directory
fallback by default; enable shared memory only after provisioning adequate
`/dev/shm` capacity. Monitor container-wide memory
and temporary storage, not only Go runtime metrics. Additional Prometheus series
include browser launches, admission rejections/reserved bytes, render queue time,
rendering, post-processing, batch, delivery, and total async durations.

Run checks with `go test -race ./...` and `go vet ./...`. With Chrome installed,
run browser reuse, isolation, cancellation, and crash recovery checks using:

```sh
PDFFORGE_CHROME_TESTS=1 go test -race ./internal/converters
```

See [PERFORMANCE_ASSESSMENT.md](PERFORMANCE_ASSESSMENT.md) for the original baseline
and the implementation validation results.
