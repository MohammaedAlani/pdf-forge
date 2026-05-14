package converters

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"pdf-forge/internal/models"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// PDFProcessor handles post-processing of PDFs (security, watermarks, metadata).
type PDFProcessor struct {
	baseDir string
}

// NewPDFProcessor creates a new processor.
func NewPDFProcessor() (*PDFProcessor, error) {
	baseDir, err := os.MkdirTemp("", "pdfforge-proc-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	return &PDFProcessor{baseDir: baseDir}, nil
}

// Close cleans up temporary files.
func (p *PDFProcessor) Close() error {
	return os.RemoveAll(p.baseDir)
}

func (p *PDFProcessor) scratch(op string) (string, func(), error) {
	dir, err := os.MkdirTemp(p.baseDir, op+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create scratch dir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// ApplySecurity applies password protection to a PDF using qpdf.
func (p *PDFProcessor) ApplySecurity(pdfData []byte, security *models.PDFSecurity) ([]byte, error) {
	if security == nil {
		return pdfData, nil
	}
	if security.UserPassword == "" && security.OwnerPassword == "" {
		return pdfData, nil
	}

	dir, cleanup, err := p.scratch("encrypt")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdfData, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	args := []string{
		"--encrypt",
		security.UserPassword,
		security.OwnerPassword,
	}

	keyBits := security.EncryptionBits
	if keyBits != 128 && keyBits != 256 {
		keyBits = 256
	}

	if keyBits == 256 {
		args = append(args, "256")
		if security.AllowPrinting {
			args = append(args, "--print=full")
		} else {
			args = append(args, "--print=none")
		}
		if security.AllowModifying {
			args = append(args, "--modify=all")
		} else {
			args = append(args, "--modify=none")
		}
		if security.AllowCopying {
			args = append(args, "--extract=y")
		} else {
			args = append(args, "--extract=n")
		}
	} else {
		args = append(args, "128")
		if !security.AllowPrinting {
			args = append(args, "--print=n")
		}
		if !security.AllowModifying {
			args = append(args, "--modify=n")
		}
		if !security.AllowCopying {
			args = append(args, "--extract=n")
		}
	}

	args = append(args, "--", inputPath, outputPath)

	cmd := exec.Command("qpdf", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("qpdf encryption failed: %w - %s", err, stderr.String())
	}

	return os.ReadFile(outputPath)
}

// ApplyWatermark applies a text watermark to PDF pages using pdfcpu.
func (p *PDFProcessor) ApplyWatermark(pdfData []byte, w *models.Watermark) ([]byte, error) {
	if w == nil || w.Text == "" {
		return pdfData, nil
	}

	fontSize := w.FontSize
	if fontSize <= 0 {
		fontSize = 48
	}
	opacity := w.Opacity
	if opacity <= 0 || opacity > 1 {
		opacity = 0.3
	}
	rotation := w.Rotation
	if rotation == 0 {
		rotation = 45
	}
	color := w.Color
	if color == "" {
		color = "0.5 0.5 0.5" // gray in pdfcpu RGB-fraction syntax
	}

	desc := fmt.Sprintf(
		"font:Helvetica, points:%.0f, opacity:%.2f, rotation:%.0f, color:%s, scale:1.0 abs, pos:c",
		fontSize, opacity, rotation, color,
	)

	wm, err := api.TextWatermark(w.Text, desc, true, false, types.POINTS)
	if err != nil {
		return nil, fmt.Errorf("watermark setup failed: %w", err)
	}

	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(pdfData), &out, nil, wm, model.NewDefaultConfiguration()); err != nil {
		return nil, fmt.Errorf("watermark apply failed: %w", err)
	}
	return out.Bytes(), nil
}

// SetMetadata sets PDF document properties (Title/Author/Subject/Keywords/Creator) via pdfcpu.
func (p *PDFProcessor) SetMetadata(pdfData []byte, m *models.PDFMetadata) ([]byte, error) {
	if m == nil {
		return pdfData, nil
	}

	props := map[string]string{}
	if m.Title != "" {
		props["Title"] = m.Title
	}
	if m.Author != "" {
		props["Author"] = m.Author
	}
	if m.Subject != "" {
		props["Subject"] = m.Subject
	}
	if m.Keywords != "" {
		props["Keywords"] = m.Keywords
	}
	if m.Creator != "" {
		props["Creator"] = m.Creator
	}
	if len(props) == 0 {
		return pdfData, nil
	}

	var out bytes.Buffer
	if err := api.AddProperties(bytes.NewReader(pdfData), &out, props, model.NewDefaultConfiguration()); err != nil {
		return nil, fmt.Errorf("metadata apply failed: %w", err)
	}
	return out.Bytes(), nil
}

// MergePDFs merges multiple PDFs using qpdf.
func (p *PDFProcessor) MergePDFs(pdfs [][]byte) ([]byte, error) {
	if len(pdfs) == 0 {
		return nil, fmt.Errorf("no PDFs provided for merge")
	}
	if len(pdfs) == 1 {
		return pdfs[0], nil
	}

	dir, cleanup, err := p.scratch("merge")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPaths := make([]string, 0, len(pdfs))
	for i, pdf := range pdfs {
		path := filepath.Join(dir, fmt.Sprintf("part_%d.pdf", i))
		if err := os.WriteFile(path, pdf, 0o644); err != nil {
			return nil, fmt.Errorf("failed to write part %d: %w", i, err)
		}
		inputPaths = append(inputPaths, path)
	}

	outputPath := filepath.Join(dir, "merged.pdf")
	args := []string{"--empty", "--pages"}
	args = append(args, inputPaths...)
	args = append(args, "--", outputPath)

	cmd := exec.Command("qpdf", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("PDF merge failed: %w - %s", err, stderr.String())
	}

	return os.ReadFile(outputPath)
}

// CompressPDF optimizes PDF file size using Ghostscript.
func (p *PDFProcessor) CompressPDF(pdfData []byte) ([]byte, error) {
	dir, cleanup, err := p.scratch("compress")
	if err != nil {
		return pdfData, nil
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdfData, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	args := []string{
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=/ebook",
		"-dNOPAUSE",
		"-dQUIET",
		"-dBATCH",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	}

	cmd := exec.Command("gs", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return pdfData, nil
	}

	compressed, err := os.ReadFile(outputPath)
	if err != nil {
		return pdfData, nil
	}
	if len(compressed) < len(pdfData) {
		return compressed, nil
	}
	return pdfData, nil
}

// Process applies the post-processing pipeline (watermark, metadata, security)
// in an order that prevents later steps from invalidating earlier ones.
func (p *PDFProcessor) Process(pdfData []byte, opts *models.PDFOptions) ([]byte, error) {
	if opts == nil {
		return pdfData, nil
	}

	var err error

	if opts.Watermark != nil && opts.Watermark.Text != "" {
		pdfData, err = p.ApplyWatermark(pdfData, opts.Watermark)
		if err != nil {
			return nil, fmt.Errorf("watermark failed: %w", err)
		}
	}

	if opts.Metadata != nil {
		pdfData, err = p.SetMetadata(pdfData, opts.Metadata)
		if err != nil {
			return nil, fmt.Errorf("metadata failed: %w", err)
		}
	}

	// Security must run last; encryption would block subsequent edits.
	if opts.Security != nil {
		pdfData, err = p.ApplySecurity(pdfData, opts.Security)
		if err != nil {
			return nil, fmt.Errorf("security failed: %w", err)
		}
	}

	return pdfData, nil
}

// ConvertToPDFA converts PDF to PDF/A format for archival.
func (p *PDFProcessor) ConvertToPDFA(pdfData []byte) ([]byte, error) {
	dir, cleanup, err := p.scratch("pdfa")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdfData, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	args := []string{
		"-dPDFA=2",
		"-dBATCH",
		"-dNOPAUSE",
		"-dNOOUTERSAVE",
		"-sDEVICE=pdfwrite",
		"-sColorConversionStrategy=UseDeviceIndependentColor",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	}

	cmd := exec.Command("gs", args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("PDF/A conversion failed: %w", err)
	}

	return os.ReadFile(outputPath)
}

// AddPageNumbersWithPDFCPU stamps page numbers via pdfcpu.
// Format defaults to "%p / %P" (e.g. "3 / 12"). Position defaults to "bottom-center".
func AddPageNumbersWithPDFCPU(pdfData []byte, position, format string) ([]byte, error) {
	if format == "" {
		format = "%p / %P"
	}
	pos := "bc"
	switch position {
	case "bottom-left", "bl":
		pos = "bl"
	case "bottom-right", "br":
		pos = "br"
	case "bottom-center", "bc", "":
		pos = "bc"
	case "top-left", "tl":
		pos = "tl"
	case "top-right", "tr":
		pos = "tr"
	case "top-center", "tc":
		pos = "tc"
	}

	desc := fmt.Sprintf("font:Helvetica, points:10, opacity:1.0, rotation:0, scale:1.0 abs, pos:%s", pos)
	wm, err := api.TextWatermark(format, desc, true, false, types.POINTS)
	if err != nil {
		return nil, fmt.Errorf("page-number setup failed: %w", err)
	}

	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(pdfData), &out, nil, wm, model.NewDefaultConfiguration()); err != nil {
		return nil, fmt.Errorf("page-number stamping failed: %w", err)
	}
	return out.Bytes(), nil
}

// StreamingCopy copies PDF data efficiently.
func StreamingCopy(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}
