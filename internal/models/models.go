package models

// ConversionType defines the type of conversion
type ConversionType string

const (
	ConvertHTML     ConversionType = "html"
	ConvertImage    ConversionType = "image"
	ConvertImages   ConversionType = "images"
	ConvertURL      ConversionType = "url"
	ConvertMarkdown ConversionType = "markdown"
	ConvertMerge    ConversionType = "merge"
)

// PageSize represents standard page sizes
type PageSize string

const (
	PageA4      PageSize = "A4"
	PageA3      PageSize = "A3"
	PageLetter  PageSize = "Letter"
	PageLegal   PageSize = "Legal"
	PageTabloid PageSize = "Tabloid"
	PageCustom  PageSize = "Custom"
)

// Orientation for PDF pages
type Orientation string

const (
	Portrait  Orientation = "portrait"
	Landscape Orientation = "landscape"
)

// PageDimensions holds width and height in inches
type PageDimensions struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// GetDimensions returns dimensions for standard page sizes
func (p PageSize) GetDimensions() PageDimensions {
	switch p {
	case PageA4:
		return PageDimensions{Width: 8.27, Height: 11.69}
	case PageA3:
		return PageDimensions{Width: 11.69, Height: 16.54}
	case PageLetter:
		return PageDimensions{Width: 8.5, Height: 11}
	case PageLegal:
		return PageDimensions{Width: 8.5, Height: 14}
	case PageTabloid:
		return PageDimensions{Width: 11, Height: 17}
	default:
		return PageDimensions{Width: 8.27, Height: 11.69}
	}
}

// Margins for PDF pages (in inches)
type Margins struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// DefaultMargins returns sensible default margins
func DefaultMargins() Margins {
	return Margins{Top: 0.4, Bottom: 0.4, Left: 0.4, Right: 0.4}
}

// PDFSecurity holds security/encryption settings
type PDFSecurity struct {
	UserPassword   string `json:"user_password,omitempty"`   // Password to open PDF
	OwnerPassword  string `json:"owner_password,omitempty"`  // Password for full access
	AllowPrinting  bool   `json:"allow_printing"`            // Allow printing
	AllowCopying   bool   `json:"allow_copying"`             // Allow copy/paste
	AllowModifying bool   `json:"allow_modifying"`           // Allow modifications
	EncryptionBits int    `json:"encryption_bits,omitempty"` // 128 or 256
}

// PDFMetadata holds document metadata
type PDFMetadata struct {
	Title    string `json:"title,omitempty"`
	Author   string `json:"author,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Keywords string `json:"keywords,omitempty"`
	Creator  string `json:"creator,omitempty"`
}

// Watermark configuration
type Watermark struct {
	Text     string  `json:"text,omitempty"`
	FontSize float64 `json:"font_size,omitempty"`
	Opacity  float64 `json:"opacity,omitempty"` // 0.0 to 1.0
	Rotation float64 `json:"rotation,omitempty"`
	Color    string  `json:"color,omitempty"` // Hex color
}

// HeaderFooter configuration
type HeaderFooter struct {
	HeaderLeft   string  `json:"header_left,omitempty"`
	HeaderCenter string  `json:"header_center,omitempty"`
	HeaderRight  string  `json:"header_right,omitempty"`
	FooterLeft   string  `json:"footer_left,omitempty"`
	FooterCenter string  `json:"footer_center,omitempty"`
	FooterRight  string  `json:"footer_right,omitempty"`
	FontSize     float64 `json:"font_size,omitempty"`
}

// PDFOptions contains all PDF generation options
type PDFOptions struct {
	PageSize         PageSize        `json:"page_size,omitempty"`
	CustomDimensions *PageDimensions `json:"custom_dimensions,omitempty"`
	Orientation      Orientation     `json:"orientation,omitempty"`
	Margins          *Margins        `json:"margins,omitempty"`
	Security         *PDFSecurity    `json:"security,omitempty"`
	Metadata         *PDFMetadata    `json:"metadata,omitempty"`
	Watermark        *Watermark      `json:"watermark,omitempty"`
	HeaderFooter     *HeaderFooter   `json:"header_footer,omitempty"`
	PrintBackground  bool            `json:"print_background"`
	Scale            float64         `json:"scale,omitempty"` // 0.1 to 2.0
	Grayscale        bool            `json:"grayscale,omitempty"`
}

// DefaultOptions returns sensible defaults
func DefaultOptions() PDFOptions {
	return PDFOptions{
		PageSize:        PageA4,
		Orientation:     Portrait,
		Margins:         &Margins{Top: 0.4, Bottom: 0.4, Left: 0.4, Right: 0.4},
		PrintBackground: true,
		Scale:           1.0,
	}
}

// ConversionRequest is the main request structure
type ConversionRequest struct {
	Type ConversionType `json:"type"`

	// For HTML conversion
	HTML     string `json:"html,omitempty"`
	IsBase64 bool   `json:"is_base64,omitempty"`

	// For URL conversion
	URL string `json:"url,omitempty"`

	// For Markdown conversion
	Markdown string `json:"markdown,omitempty"`

	// For Image conversion (base64 encoded)
	Image  string   `json:"image,omitempty"`
	Images []string `json:"images,omitempty"`

	// For PDF merge
	PDFs []string `json:"pdfs,omitempty"` // Base64 encoded PDFs

	// Common options
	Options *PDFOptions `json:"options,omitempty"`
}

// ConversionResponse for async operations
type ConversionResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HealthResponse for health check endpoint
type HealthResponse struct {
	Status      string            `json:"status"`
	Version     string            `json:"version"`
	Uptime      string            `json:"uptime"`
	Workers     WorkerStatus      `json:"workers"`
	Chrome      string            `json:"chrome"`
	Conversions ConversionMetrics `json:"conversions"`
}

// WorkerStatus shows worker pool status
type WorkerStatus struct {
	Max       int `json:"max"`
	Available int `json:"available"`
	InUse     int `json:"in_use"`
}

// ConversionMetrics tracks conversion statistics
type ConversionMetrics struct {
	Total      int64            `json:"total"`
	Successful int64            `json:"successful"`
	Failed     int64            `json:"failed"`
	ByType     map[string]int64 `json:"by_type"`
}

// PDFInfo contains PDF metadata and information
type PDFInfo struct {
	Title      string `json:"title,omitempty"`
	Author     string `json:"author,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Keywords   string `json:"keywords,omitempty"`
	Creator    string `json:"creator,omitempty"`
	Producer   string `json:"producer,omitempty"`
	PageCount  int    `json:"page_count"`
	PageSize   string `json:"page_size,omitempty"`
	PDFVersion string `json:"pdf_version,omitempty"`
	Encrypted  bool   `json:"encrypted"`
	FileSize   int64  `json:"file_size"`
}

// TemplateRequest for template-based PDF generation
type TemplateRequest struct {
	Template   string                 `json:"template"`   // invoice, receipt, certificate, report, contract, custom
	CustomHTML string                 `json:"custom_html,omitempty"` // For custom template
	Data       map[string]interface{} `json:"data"`       // Template variables
	Options    *PDFOptions            `json:"options,omitempty"`
}

// WebhookConfig for async processing callbacks
type WebhookConfig struct {
	URL         string            `json:"url"`
	Method      string            `json:"method,omitempty"` // POST (default), PUT
	Headers     map[string]string `json:"headers,omitempty"`
	Secret      string            `json:"secret,omitempty"` // For HMAC signature
	RetryCount  int               `json:"retry_count,omitempty"`
	IncludePDF  bool              `json:"include_pdf,omitempty"` // Include PDF in webhook (base64)
}

// AsyncRequest for background processing
type AsyncRequest struct {
	Request ConversionRequest `json:"request"`
	Webhook *WebhookConfig    `json:"webhook,omitempty"`
	Storage *StorageConfig    `json:"storage,omitempty"`
}

// StorageConfig for cloud storage upload
type StorageConfig struct {
	Provider    string            `json:"provider"` // s3, gcs, azure, local
	Bucket      string            `json:"bucket"`
	Path        string            `json:"path"`
	Filename    string            `json:"filename,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	ACL         string            `json:"acl,omitempty"` // private, public-read
	Metadata    map[string]string `json:"metadata,omitempty"`
	
	// S3-specific
	Region          string `json:"region,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"` // For S3-compatible storage
}

// StorageResult contains upload result
type StorageResult struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Path     string `json:"path"`
	URL      string `json:"url,omitempty"`
	Size     int64  `json:"size"`
}

// ManipulateRequest for PDF manipulation operations
type ManipulateRequest struct {
	Operation string      `json:"operation"` // split, extract, rotate, compress, info, remove, reorder, to_images
	PDF       string      `json:"pdf"`       // Base64 encoded PDF
	Options   *ManipulateOptions `json:"options,omitempty"`
}

// ManipulateOptions for manipulation operations
type ManipulateOptions struct {
	// For split
	SplitType string `json:"split_type,omitempty"` // all, range, every_n
	EveryN    int    `json:"every_n,omitempty"`
	
	// For extract, remove, rotate
	Pages string `json:"pages,omitempty"` // "1-3,5,7-9"
	
	// For rotate
	Rotation int `json:"rotation,omitempty"` // 90, 180, 270
	
	// For compress
	CompressionLevel string `json:"compression_level,omitempty"` // screen, ebook, printer, prepress
	
	// For reorder
	NewOrder []int `json:"new_order,omitempty"`
	
	// For to_images
	ImageFormat string `json:"image_format,omitempty"` // jpeg, png
	DPI         int    `json:"dpi,omitempty"`
}

// ManipulateResult contains operation result
type ManipulateResult struct {
	Operation string   `json:"operation"`
	Success   bool     `json:"success"`
	Message   string   `json:"message,omitempty"`
	
	// For single PDF output
	PDF string `json:"pdf,omitempty"` // Base64 encoded
	
	// For multiple outputs (split, to_images)
	Files []string `json:"files,omitempty"` // Base64 encoded
	Count int      `json:"count,omitempty"`
	
	// For info operation
	Info *PDFInfo `json:"info,omitempty"`
	
	// For compress
	OriginalSize   int64 `json:"original_size,omitempty"`
	CompressedSize int64 `json:"compressed_size,omitempty"`
	SavingsPercent int   `json:"savings_percent,omitempty"`
}

// BatchRequest for processing multiple conversions
type BatchRequest struct {
	Requests []ConversionRequest `json:"requests"`
	Merge    bool                `json:"merge,omitempty"` // Merge all results into one PDF
	Webhook  *WebhookConfig      `json:"webhook,omitempty"`
}

// BatchResult contains batch processing results
type BatchResult struct {
	RequestID string          `json:"request_id"`
	Total     int             `json:"total"`
	Completed int             `json:"completed"`
	Failed    int             `json:"failed"`
	Results   []BatchItemResult `json:"results,omitempty"`
	MergedPDF string          `json:"merged_pdf,omitempty"` // Base64 if merge=true
}

// BatchItemResult for individual batch item
type BatchItemResult struct {
	Index   int    `json:"index"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	PDF     string `json:"pdf,omitempty"` // Base64 encoded
	Size    int64  `json:"size,omitempty"`
}

// TableData for CSV/JSON to PDF table conversion
type TableData struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	Title   string     `json:"title,omitempty"`
	Footer  string     `json:"footer,omitempty"`
}

// ChartConfig for embedding charts in PDFs
type ChartConfig struct {
	Type   string                 `json:"type"` // bar, line, pie, doughnut
	Data   map[string]interface{} `json:"data"`
	Width  int                    `json:"width,omitempty"`
	Height int                    `json:"height,omitempty"`
}
