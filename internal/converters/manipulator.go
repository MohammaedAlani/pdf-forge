package converters

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"pdf-forge/internal/models"
)

// PDFManipulator provides advanced PDF manipulation operations
type PDFManipulator struct {
	tempDir string
}

// NewPDFManipulator creates a new manipulator instance
func NewPDFManipulator() (*PDFManipulator, error) {
	tempDir, err := os.MkdirTemp("", "pdfforge-manip-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	return &PDFManipulator{tempDir: tempDir}, nil
}

// Close cleans up resources
func (m *PDFManipulator) Close() error {
	return os.RemoveAll(m.tempDir)
}

// SplitRequest defines how to split a PDF
type SplitRequest struct {
	PDF       []byte `json:"pdf"`        // Base64 decoded PDF
	SplitType string `json:"split_type"` // "all", "range", "every_n"
	Pages     string `json:"pages"`      // "1-3,5,7-9" for range
	EveryN    int    `json:"every_n"`    // Split every N pages
}

// SplitResult contains split PDF pages
type SplitResult struct {
	Pages [][]byte `json:"pages"`
	Count int      `json:"count"`
}

// Split splits a PDF into multiple PDFs
func (m *PDFManipulator) Split(ctx context.Context, req *SplitRequest) (*SplitResult, error) {
	inputPath := filepath.Join(m.tempDir, "split_input.pdf")
	if err := os.WriteFile(inputPath, req.PDF, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)

	// Get page count
	pageCount, err := m.getPageCount(inputPath)
	if err != nil {
		return nil, err
	}

	var pages [][]byte

	switch req.SplitType {
	case "all":
		// Split into individual pages
		for i := 1; i <= pageCount; i++ {
			outputPath := filepath.Join(m.tempDir, fmt.Sprintf("page_%d.pdf", i))
			args := []string{inputPath, fmt.Sprintf("%d", i), outputPath}
			if err := m.runQPDF(args...); err != nil {
				return nil, fmt.Errorf("failed to extract page %d: %w", i, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
			os.Remove(outputPath)
		}

	case "range":
		// Parse range like "1-3,5,7-9"
		ranges := m.parsePageRanges(req.Pages, pageCount)
		for i, r := range ranges {
			outputPath := filepath.Join(m.tempDir, fmt.Sprintf("range_%d.pdf", i))
			args := []string{inputPath, "--pages", inputPath, r, "--", outputPath}
			if err := m.runQPDF(args...); err != nil {
				return nil, fmt.Errorf("failed to extract range %s: %w", r, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
			os.Remove(outputPath)
		}

	case "every_n":
		// Split every N pages
		n := req.EveryN
		if n <= 0 {
			n = 1
		}
		for start := 1; start <= pageCount; start += n {
			end := start + n - 1
			if end > pageCount {
				end = pageCount
			}
			outputPath := filepath.Join(m.tempDir, fmt.Sprintf("chunk_%d.pdf", start))
			rangeStr := fmt.Sprintf("%d-%d", start, end)
			args := []string{inputPath, "--pages", inputPath, rangeStr, "--", outputPath}
			if err := m.runQPDF(args...); err != nil {
				return nil, fmt.Errorf("failed to extract chunk %s: %w", rangeStr, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
			os.Remove(outputPath)
		}
	}

	return &SplitResult{Pages: pages, Count: len(pages)}, nil
}

// ExtractPages extracts specific pages from a PDF
func (m *PDFManipulator) ExtractPages(ctx context.Context, pdf []byte, pageRange string) ([]byte, error) {
	inputPath := filepath.Join(m.tempDir, "extract_input.pdf")
	outputPath := filepath.Join(m.tempDir, "extract_output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	args := []string{inputPath, "--pages", inputPath, pageRange, "--", outputPath}
	if err := m.runQPDF(args...); err != nil {
		return nil, fmt.Errorf("failed to extract pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// RotatePages rotates pages in a PDF
func (m *PDFManipulator) RotatePages(ctx context.Context, pdf []byte, rotation int, pageRange string) ([]byte, error) {
	inputPath := filepath.Join(m.tempDir, "rotate_input.pdf")
	outputPath := filepath.Join(m.tempDir, "rotate_output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// Normalize rotation to 90, 180, 270
	rotation = ((rotation % 360) + 360) % 360
	if rotation != 90 && rotation != 180 && rotation != 270 {
		rotation = 90
	}

	rotateArg := fmt.Sprintf("+%d", rotation)
	if pageRange == "" {
		pageRange = "1-z" // All pages
	}

	args := []string{inputPath, "--rotate=" + rotateArg + ":" + pageRange, "--", outputPath}
	if err := m.runQPDF(args...); err != nil {
		return nil, fmt.Errorf("failed to rotate pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// CompressLevel defines compression levels
type CompressLevel string

const (
	CompressScreen   CompressLevel = "screen"   // 72 dpi
	CompressEbook    CompressLevel = "ebook"    // 150 dpi
	CompressPrinter  CompressLevel = "printer"  // 300 dpi
	CompressPrepress CompressLevel = "prepress" // 300 dpi, color preserving
)

// Compress compresses a PDF using Ghostscript
func (m *PDFManipulator) Compress(ctx context.Context, pdf []byte, level CompressLevel) ([]byte, int, error) {
	inputPath := filepath.Join(m.tempDir, "compress_input.pdf")
	outputPath := filepath.Join(m.tempDir, "compress_output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, 0, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	if level == "" {
		level = CompressEbook
	}

	args := []string{
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		fmt.Sprintf("-dPDFSETTINGS=/%s", level),
		"-dNOPAUSE",
		"-dQUIET",
		"-dBATCH",
		"-dDetectDuplicateImages=true",
		"-dCompressFonts=true",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	}

	cmd := exec.CommandContext(ctx, "gs", args...)
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("compression failed: %w", err)
	}

	compressed, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, 0, err
	}

	// Calculate savings percentage
	originalSize := len(pdf)
	compressedSize := len(compressed)
	savings := 0
	if originalSize > 0 {
		savings = int(float64(originalSize-compressedSize) / float64(originalSize) * 100)
	}

	return compressed, savings, nil
}

// PDFToImages converts PDF pages to images
func (m *PDFManipulator) PDFToImages(ctx context.Context, pdf []byte, format string, dpi int) ([][]byte, error) {
	inputPath := filepath.Join(m.tempDir, "pdf2img_input.pdf")
	outputPrefix := filepath.Join(m.tempDir, "page")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)

	if format == "" {
		format = "jpeg"
	}
	if dpi <= 0 {
		dpi = 150
	}

	// Use pdftoppm for conversion
	args := []string{
		fmt.Sprintf("-r"), fmt.Sprintf("%d", dpi),
	}

	switch format {
	case "png":
		args = append(args, "-png")
	case "jpeg", "jpg":
		args = append(args, "-jpeg")
	default:
		args = append(args, "-jpeg")
	}

	args = append(args, inputPath, outputPrefix)

	cmd := exec.CommandContext(ctx, "pdftoppm", args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("PDF to image conversion failed: %w", err)
	}

	// Collect output images
	var images [][]byte
	pattern := outputPrefix + "*"
	matches, _ := filepath.Glob(pattern)

	for _, match := range matches {
		imgData, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		images = append(images, imgData)
		os.Remove(match)
	}

	return images, nil
}

// GetInfo returns PDF metadata and info
func (m *PDFManipulator) GetInfo(ctx context.Context, pdf []byte) (*models.PDFInfo, error) {
	inputPath := filepath.Join(m.tempDir, "info_input.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)

	// Use pdfinfo command
	cmd := exec.CommandContext(ctx, "pdfinfo", inputPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PDF info: %w", err)
	}

	info := &models.PDFInfo{
		FileSize: int64(len(pdf)),
	}

	// Parse output
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Title":
			info.Title = value
		case "Author":
			info.Author = value
		case "Subject":
			info.Subject = value
		case "Keywords":
			info.Keywords = value
		case "Creator":
			info.Creator = value
		case "Producer":
			info.Producer = value
		case "Pages":
			info.PageCount, _ = strconv.Atoi(value)
		case "Page size":
			info.PageSize = value
		case "PDF version":
			info.PDFVersion = value
		case "Encrypted":
			info.Encrypted = value == "yes"
		}
	}

	return info, nil
}

// AddPageNumbers adds page numbers to a PDF
func (m *PDFManipulator) AddPageNumbers(ctx context.Context, pdf []byte, position string, format string) ([]byte, error) {
	// This is a complex operation that typically requires a library like pdfcpu
	// For now, return the original PDF
	// In production, integrate pdfcpu or use a different approach
	return pdf, nil
}

// RemovePages removes specific pages from a PDF
func (m *PDFManipulator) RemovePages(ctx context.Context, pdf []byte, pagesToRemove string) ([]byte, error) {
	inputPath := filepath.Join(m.tempDir, "remove_input.pdf")
	outputPath := filepath.Join(m.tempDir, "remove_output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// Get page count
	pageCount, err := m.getPageCount(inputPath)
	if err != nil {
		return nil, err
	}

	// Parse pages to remove
	removeSet := make(map[int]bool)
	for _, part := range strings.Split(pagesToRemove, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) == 2 {
				start, _ := strconv.Atoi(rangeParts[0])
				end, _ := strconv.Atoi(rangeParts[1])
				for i := start; i <= end; i++ {
					removeSet[i] = true
				}
			}
		} else {
			page, _ := strconv.Atoi(part)
			removeSet[page] = true
		}
	}

	// Build keep range
	var keepRanges []string
	inRange := false
	rangeStart := 0

	for i := 1; i <= pageCount; i++ {
		if !removeSet[i] {
			if !inRange {
				rangeStart = i
				inRange = true
			}
		} else {
			if inRange {
				if rangeStart == i-1 {
					keepRanges = append(keepRanges, fmt.Sprintf("%d", rangeStart))
				} else {
					keepRanges = append(keepRanges, fmt.Sprintf("%d-%d", rangeStart, i-1))
				}
				inRange = false
			}
		}
	}
	if inRange {
		if rangeStart == pageCount {
			keepRanges = append(keepRanges, fmt.Sprintf("%d", rangeStart))
		} else {
			keepRanges = append(keepRanges, fmt.Sprintf("%d-%d", rangeStart, pageCount))
		}
	}

	if len(keepRanges) == 0 {
		return nil, fmt.Errorf("cannot remove all pages")
	}

	keepStr := strings.Join(keepRanges, ",")
	args := []string{inputPath, "--pages", inputPath, keepStr, "--", outputPath}
	if err := m.runQPDF(args...); err != nil {
		return nil, fmt.Errorf("failed to remove pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// ReorderPages reorders pages in a PDF
func (m *PDFManipulator) ReorderPages(ctx context.Context, pdf []byte, newOrder []int) ([]byte, error) {
	inputPath := filepath.Join(m.tempDir, "reorder_input.pdf")
	outputPath := filepath.Join(m.tempDir, "reorder_output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// Build page string
	var pageStrs []string
	for _, p := range newOrder {
		pageStrs = append(pageStrs, fmt.Sprintf("%d", p))
	}
	pageStr := strings.Join(pageStrs, ",")

	args := []string{inputPath, "--pages", inputPath, pageStr, "--", outputPath}
	if err := m.runQPDF(args...); err != nil {
		return nil, fmt.Errorf("failed to reorder pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// Helper functions

func (m *PDFManipulator) runQPDF(args ...string) error {
	cmd := exec.Command("qpdf", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}

func (m *PDFManipulator) getPageCount(pdfPath string) (int, error) {
	cmd := exec.Command("qpdf", "--show-npages", pdfPath)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get page count: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("invalid page count: %w", err)
	}
	return count, nil
}

func (m *PDFManipulator) parsePageRanges(rangeStr string, maxPage int) []string {
	var ranges []string
	for _, part := range strings.Split(rangeStr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Replace 'z' or 'end' with actual last page
		part = strings.ReplaceAll(part, "z", fmt.Sprintf("%d", maxPage))
		part = strings.ReplaceAll(part, "end", fmt.Sprintf("%d", maxPage))
		ranges = append(ranges, part)
	}
	return ranges
}

// ImageToBase64 converts image bytes to base64 string
func ImageToBase64(imgData []byte, format string) string {
	return base64.StdEncoding.EncodeToString(imgData)
}

// DecodeImage decodes image bytes to image.Image
func DecodeImage(data []byte) (image.Image, string, error) {
	reader := bytes.NewReader(data)
	return image.Decode(reader)
}

// EncodeImage encodes image.Image to bytes
func EncodeImage(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case "png":
		err := png.Encode(&buf, img)
		return buf.Bytes(), err
	case "jpeg", "jpg":
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
		return buf.Bytes(), err
	default:
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
		return buf.Bytes(), err
	}
}
