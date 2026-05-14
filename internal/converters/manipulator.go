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

// PDFManipulator provides advanced PDF manipulation operations.
//
// Each public method creates an isolated subdirectory under `baseDir` so that
// concurrent requests cannot clobber each other's temp files.
type PDFManipulator struct {
	baseDir string
}

// NewPDFManipulator creates a new manipulator instance.
func NewPDFManipulator() (*PDFManipulator, error) {
	baseDir, err := os.MkdirTemp("", "pdfforge-manip-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	return &PDFManipulator{baseDir: baseDir}, nil
}

// Close removes the manipulator's base temp directory.
func (m *PDFManipulator) Close() error {
	return os.RemoveAll(m.baseDir)
}

// scratch returns an empty subdirectory unique to a single operation along
// with a cleanup function that removes it.
func (m *PDFManipulator) scratch(op string) (string, func(), error) {
	dir, err := os.MkdirTemp(m.baseDir, op+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create scratch dir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// SplitRequest defines how to split a PDF
type SplitRequest struct {
	PDF       []byte `json:"pdf"`        // Decoded PDF bytes
	SplitType string `json:"split_type"` // "all", "range", "every_n"
	Pages     string `json:"pages"`      // "1-3,5,7-9" for range
	EveryN    int    `json:"every_n"`    // Split every N pages
}

// SplitResult contains split PDF pages
type SplitResult struct {
	Pages [][]byte `json:"pages"`
	Count int      `json:"count"`
}

// Split splits a PDF into multiple PDFs.
func (m *PDFManipulator) Split(ctx context.Context, req *SplitRequest) (*SplitResult, error) {
	dir, cleanup, err := m.scratch("split")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	if err := os.WriteFile(inputPath, req.PDF, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	pageCount, err := m.getPageCount(ctx, inputPath)
	if err != nil {
		return nil, err
	}

	var pages [][]byte

	switch req.SplitType {
	case "all":
		for i := 1; i <= pageCount; i++ {
			outputPath := filepath.Join(dir, fmt.Sprintf("page_%d.pdf", i))
			args := []string{inputPath, fmt.Sprintf("%d", i), outputPath}
			if err := m.runQPDF(ctx, args...); err != nil {
				return nil, fmt.Errorf("failed to extract page %d: %w", i, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
		}

	case "range":
		ranges := m.parsePageRanges(req.Pages, pageCount)
		for i, r := range ranges {
			outputPath := filepath.Join(dir, fmt.Sprintf("range_%d.pdf", i))
			args := []string{inputPath, "--pages", inputPath, r, "--", outputPath}
			if err := m.runQPDF(ctx, args...); err != nil {
				return nil, fmt.Errorf("failed to extract range %s: %w", r, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
		}

	case "every_n":
		n := req.EveryN
		if n <= 0 {
			n = 1
		}
		for start := 1; start <= pageCount; start += n {
			end := start + n - 1
			if end > pageCount {
				end = pageCount
			}
			outputPath := filepath.Join(dir, fmt.Sprintf("chunk_%d.pdf", start))
			rangeStr := fmt.Sprintf("%d-%d", start, end)
			args := []string{inputPath, "--pages", inputPath, rangeStr, "--", outputPath}
			if err := m.runQPDF(ctx, args...); err != nil {
				return nil, fmt.Errorf("failed to extract chunk %s: %w", rangeStr, err)
			}
			pageData, err := os.ReadFile(outputPath)
			if err != nil {
				return nil, err
			}
			pages = append(pages, pageData)
		}

	default:
		return nil, fmt.Errorf("unknown split_type: %q (expected all, range, every_n)", req.SplitType)
	}

	return &SplitResult{Pages: pages, Count: len(pages)}, nil
}

// ExtractPages extracts specific pages from a PDF.
func (m *PDFManipulator) ExtractPages(ctx context.Context, pdf []byte, pageRange string) ([]byte, error) {
	dir, cleanup, err := m.scratch("extract")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	args := []string{inputPath, "--pages", inputPath, pageRange, "--", outputPath}
	if err := m.runQPDF(ctx, args...); err != nil {
		return nil, fmt.Errorf("failed to extract pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// RotatePages rotates pages in a PDF.
func (m *PDFManipulator) RotatePages(ctx context.Context, pdf []byte, rotation int, pageRange string) ([]byte, error) {
	dir, cleanup, err := m.scratch("rotate")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	rotation = ((rotation % 360) + 360) % 360
	if rotation != 90 && rotation != 180 && rotation != 270 {
		rotation = 90
	}

	rotateArg := fmt.Sprintf("+%d", rotation)
	if pageRange == "" {
		pageRange = "1-z"
	}

	args := []string{inputPath, "--rotate=" + rotateArg + ":" + pageRange, "--", outputPath}
	if err := m.runQPDF(ctx, args...); err != nil {
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

// Compress compresses a PDF using Ghostscript.
func (m *PDFManipulator) Compress(ctx context.Context, pdf []byte, level CompressLevel) ([]byte, int, error) {
	dir, cleanup, err := m.scratch("compress")
	if err != nil {
		return nil, 0, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, 0, fmt.Errorf("failed to write input: %w", err)
	}

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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("compression failed: %w - %s", err, stderr.String())
	}

	compressed, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, 0, err
	}

	originalSize := len(pdf)
	compressedSize := len(compressed)
	savings := 0
	if originalSize > 0 {
		savings = int(float64(originalSize-compressedSize) / float64(originalSize) * 100)
	}

	return compressed, savings, nil
}

// PDFToImages converts PDF pages to images.
func (m *PDFManipulator) PDFToImages(ctx context.Context, pdf []byte, format string, dpi int) ([][]byte, error) {
	dir, cleanup, err := m.scratch("toimages")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPrefix := filepath.Join(dir, "page")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	if format == "" {
		format = "jpeg"
	}
	if dpi <= 0 {
		dpi = 150
	}

	args := []string{"-r", strconv.Itoa(dpi)}
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("PDF to image conversion failed: %w - %s", err, stderr.String())
	}

	matches, _ := filepath.Glob(outputPrefix + "*")
	var images [][]byte
	for _, match := range matches {
		imgData, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		images = append(images, imgData)
	}

	return images, nil
}

// GetInfo returns PDF metadata and info via pdfinfo.
func (m *PDFManipulator) GetInfo(ctx context.Context, pdf []byte) (*models.PDFInfo, error) {
	dir, cleanup, err := m.scratch("info")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "pdfinfo", inputPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PDF info: %w", err)
	}

	info := &models.PDFInfo{FileSize: int64(len(pdf))}

	for _, line := range strings.Split(string(output), "\n") {
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
			info.Encrypted = !strings.HasPrefix(value, "no")
		}
	}

	return info, nil
}

// RemovePages removes specific pages from a PDF.
func (m *PDFManipulator) RemovePages(ctx context.Context, pdf []byte, pagesToRemove string) ([]byte, error) {
	dir, cleanup, err := m.scratch("remove")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	pageCount, err := m.getPageCount(ctx, inputPath)
	if err != nil {
		return nil, err
	}

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

	var keepRanges []string
	inRange := false
	rangeStart := 0

	for i := 1; i <= pageCount; i++ {
		if !removeSet[i] {
			if !inRange {
				rangeStart = i
				inRange = true
			}
		} else if inRange {
			if rangeStart == i-1 {
				keepRanges = append(keepRanges, strconv.Itoa(rangeStart))
			} else {
				keepRanges = append(keepRanges, fmt.Sprintf("%d-%d", rangeStart, i-1))
			}
			inRange = false
		}
	}
	if inRange {
		if rangeStart == pageCount {
			keepRanges = append(keepRanges, strconv.Itoa(rangeStart))
		} else {
			keepRanges = append(keepRanges, fmt.Sprintf("%d-%d", rangeStart, pageCount))
		}
	}

	if len(keepRanges) == 0 {
		return nil, fmt.Errorf("cannot remove all pages")
	}

	keepStr := strings.Join(keepRanges, ",")
	args := []string{inputPath, "--pages", inputPath, keepStr, "--", outputPath}
	if err := m.runQPDF(ctx, args...); err != nil {
		return nil, fmt.Errorf("failed to remove pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// ReorderPages reorders pages in a PDF.
func (m *PDFManipulator) ReorderPages(ctx context.Context, pdf []byte, newOrder []int) ([]byte, error) {
	dir, cleanup, err := m.scratch("reorder")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inputPath := filepath.Join(dir, "input.pdf")
	outputPath := filepath.Join(dir, "output.pdf")

	if err := os.WriteFile(inputPath, pdf, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write input: %w", err)
	}

	pageStrs := make([]string, 0, len(newOrder))
	for _, p := range newOrder {
		pageStrs = append(pageStrs, strconv.Itoa(p))
	}
	pageStr := strings.Join(pageStrs, ",")

	args := []string{inputPath, "--pages", inputPath, pageStr, "--", outputPath}
	if err := m.runQPDF(ctx, args...); err != nil {
		return nil, fmt.Errorf("failed to reorder pages: %w", err)
	}

	return os.ReadFile(outputPath)
}

// Helpers

func (m *PDFManipulator) runQPDF(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "qpdf", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}

func (m *PDFManipulator) getPageCount(ctx context.Context, pdfPath string) (int, error) {
	cmd := exec.CommandContext(ctx, "qpdf", "--show-npages", pdfPath)
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
		part = strings.ReplaceAll(part, "z", strconv.Itoa(maxPage))
		part = strings.ReplaceAll(part, "end", strconv.Itoa(maxPage))
		ranges = append(ranges, part)
	}
	return ranges
}

// ImageToBase64 converts image bytes to base64 string.
func ImageToBase64(imgData []byte, format string) string {
	return base64.StdEncoding.EncodeToString(imgData)
}

// DecodeImage decodes image bytes to image.Image.
func DecodeImage(data []byte) (image.Image, string, error) {
	reader := bytes.NewReader(data)
	return image.Decode(reader)
}

// EncodeImage encodes image.Image to bytes.
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

// AddPageNumbers adds page numbers via pdfcpu (text stamp).
// Position can be "bottom-center" (default), "bottom-left", "bottom-right",
// "top-center", "top-left", "top-right".
func (m *PDFManipulator) AddPageNumbers(ctx context.Context, pdf []byte, position, format string) ([]byte, error) {
	return AddPageNumbersWithPDFCPU(pdf, position, format)
}
