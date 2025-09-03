package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config holds the configuration for image processing
type Config struct {
	ResizePercent int
	Quality       int
	BackupDir     string
}

// EPUBProcessor handles EPUB file processing
type EPUBProcessor struct {
	config Config
	stats  ProcessingStats
}

// ProcessingStats tracks processing statistics
type ProcessingStats struct {
	TotalImages     int
	ProcessedImages int
	FailedImages    int
	OriginalSize    int64
	NewSize         int64
}

// NewEPUBProcessor creates a new processor with default config
func NewEPUBProcessor() *EPUBProcessor {
	return &EPUBProcessor{
		config: Config{
			ResizePercent: 50,
			Quality:       85,
			BackupDir:     fmt.Sprintf("originals_%s", time.Now().Format("20060102_150405")),
		},
	}
}

// checkImageMagick verifies ImageMagick is installed
func (p *EPUBProcessor) checkImageMagick() error {
	cmd := exec.Command("magick", "-version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ImageMagick not found. Please install it:\n" +
			"  macOS: brew install imagemagick\n" +
			"  Ubuntu/Debian: sudo apt-get install imagemagick\n" +
			"  Windows: Download from https://imagemagick.org")
	}
	return nil
}

// extractEPUB extracts an EPUB file to a temporary directory
func (p *EPUBProcessor) extractEPUB(epubPath string) (string, error) {
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return "", fmt.Errorf("failed to open EPUB: %w", err)
	}
	defer reader.Close()

	// Create temp directory for extraction
	tempDir := fmt.Sprintf("epub_temp_%d", time.Now().Unix())
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Extract all files
	for _, file := range reader.File {
		path := filepath.Join(tempDir, file.Name)

		// Create directory if needed
		if file.FileInfo().IsDir() {
			os.MkdirAll(path, file.Mode())
			continue
		}

		// Create all parent directories
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("failed to create directory structure: %w", err)
		}

		// Extract file
		if err := p.extractFile(file, path); err != nil {
			return "", fmt.Errorf("failed to extract %s: %w", file.Name, err)
		}
	}

	return tempDir, nil
}

// extractFile extracts a single file from the archive
func (p *EPUBProcessor) extractFile(file *zip.File, destPath string) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

// isImageFile checks if a file is an image based on extension
func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".webp"}
	for _, imgExt := range imageExts {
		if ext == imgExt {
			return true
		}
	}
	return false
}

// processImage resizes a single image using ImageMagick
func (p *EPUBProcessor) processImage(imagePath string) error {
	// Get original dimensions
	cmd := exec.Command("magick", "identify", "-format", "%wx%h %B", imagePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get image info: %w", err)
	}
	
	parts := strings.Fields(string(output))
	if len(parts) >= 1 {
		fmt.Printf("  Original: %s", parts[0])
		if len(parts) >= 2 {
			fmt.Printf(" (%s bytes)", parts[1])
		}
		fmt.Println()
	}

	// Create temporary resized file
	tempPath := imagePath + ".tmp"
	
	// Resize image
	cmd = exec.Command("magick", imagePath, 
		"-resize", fmt.Sprintf("%d%%", p.config.ResizePercent),
		"-quality", fmt.Sprintf("%d", p.config.Quality),
		tempPath)
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resize image: %w", err)
	}

	// Get new dimensions
	cmd = exec.Command("magick", "identify", "-format", "%wx%h %B", tempPath)
	output, err = cmd.Output()
	if err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to get new image info: %w", err)
	}
	
	parts = strings.Fields(string(output))
	if len(parts) >= 1 {
		fmt.Printf("  New: %s", parts[0])
		if len(parts) >= 2 {
			fmt.Printf(" (%s bytes)", parts[1])
		}
		fmt.Println()
	}

	// Replace original with resized
	if err := os.Rename(tempPath, imagePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to replace original: %w", err)
	}

	return nil
}

// processImagesInDir processes all images in a directory
func (p *EPUBProcessor) processImagesInDir(dirPath string) error {
	var imageFiles []string

	// Walk through directory to find all images
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && isImageFile(path) {
			imageFiles = append(imageFiles, path)
			p.stats.TotalImages++
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	if len(imageFiles) == 0 {
		fmt.Println("No images found to process")
		return nil
	}

	fmt.Printf("\nFound %d image(s) to process\n\n", len(imageFiles))

	// Process each image
	for i, imagePath := range imageFiles {
		relPath, _ := filepath.Rel(dirPath, imagePath)
		fmt.Printf("[%d/%d] Processing: %s\n", i+1, len(imageFiles), relPath)
		
		if err := p.processImage(imagePath); err != nil {
			fmt.Printf("  ✗ Failed: %v\n", err)
			p.stats.FailedImages++
		} else {
			fmt.Printf("  ✓ Success\n")
			p.stats.ProcessedImages++
		}
		fmt.Println()
	}

	return nil
}

// createEPUB creates a new EPUB file from a directory
func (p *EPUBProcessor) createEPUB(sourceDir, outputPath string) error {
	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Create zip writer
	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	// Add mimetype file first (uncompressed, as per EPUB spec)
	mimetypePath := filepath.Join(sourceDir, "mimetype")
	if _, err := os.Stat(mimetypePath); err == nil {
		// Read mimetype content
		content, err := os.ReadFile(mimetypePath)
		if err != nil {
			return fmt.Errorf("failed to read mimetype: %w", err)
		}

		// Create uncompressed mimetype entry
		header := &zip.FileHeader{
			Name:   "mimetype",
			Method: zip.Store,
		}
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("failed to create mimetype entry: %w", err)
		}
		if _, err := writer.Write(content); err != nil {
			return fmt.Errorf("failed to write mimetype: %w", err)
		}
	}

	// Walk through directory and add all other files
	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory and mimetype (already added)
		if path == sourceDir || filepath.Base(path) == "mimetype" {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Skip directories (they're created implicitly)
		if info.IsDir() {
			return nil
		}

		// Create zip entry
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// Copy file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to add files to EPUB: %w", err)
	}

	return nil
}

// ProcessEPUBFile is the main processing function
func (p *EPUBProcessor) ProcessEPUBFile(epubPath string) error {
	fmt.Printf("Processing EPUB: %s\n", epubPath)
	fmt.Println(strings.Repeat("-", 50))

	// Check if ImageMagick is installed
	if err := p.checkImageMagick(); err != nil {
		return err
	}

	// Extract EPUB
	fmt.Println("Extracting EPUB...")
	tempDir, err := p.extractEPUB(epubPath)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	defer os.RemoveAll(tempDir) // Cleanup temp directory

	// Process images
	fmt.Println("Processing images...")
	if err := p.processImagesInDir(tempDir); err != nil {
		return fmt.Errorf("image processing failed: %w", err)
	}

	// Create output filename
	dir := filepath.Dir(epubPath)
	base := filepath.Base(epubPath)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)
	outputPath := filepath.Join(dir, fmt.Sprintf("%s_compressed%s", nameWithoutExt, ext))

	// Create new EPUB
	fmt.Println("Creating compressed EPUB...")
	if err := p.createEPUB(tempDir, outputPath); err != nil {
		return fmt.Errorf("EPUB creation failed: %w", err)
	}

	// Get file sizes for comparison
	originalInfo, _ := os.Stat(epubPath)
	newInfo, _ := os.Stat(outputPath)

	// Print summary
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println("PROCESSING COMPLETE!")
	fmt.Printf("Images processed: %d/%d\n", p.stats.ProcessedImages, p.stats.TotalImages)
	if p.stats.FailedImages > 0 {
		fmt.Printf("Failed: %d\n", p.stats.FailedImages)
	}
	fmt.Printf("Original EPUB size: %.2f MB\n", float64(originalInfo.Size())/(1024*1024))
	fmt.Printf("New EPUB size: %.2f MB\n", float64(newInfo.Size())/(1024*1024))
	fmt.Printf("Size reduction: %.1f%%\n", 
		(1-float64(newInfo.Size())/float64(originalInfo.Size()))*100)
	fmt.Printf("Output: %s\n", outputPath)

	return nil
}

// ProcessMultipleEPUBs processes multiple EPUB files
func (p *EPUBProcessor) ProcessMultipleEPUBs(pattern string) error {
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no EPUB files found matching pattern: %s", pattern)
	}

	fmt.Printf("Found %d EPUB file(s) to process\n\n", len(files))

	for i, file := range files {
		fmt.Printf("\n[%d/%d] ", i+1, len(files))
		
		// Reset stats for each file
		p.stats = ProcessingStats{}
		
		if err := p.ProcessEPUBFile(file); err != nil {
			fmt.Printf("Error processing %s: %v\n", file, err)
		}
		
		if i < len(files)-1 {
			fmt.Println("\n" + strings.Repeat("=", 50))
		}
	}

	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <epub-file-or-pattern>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s book.epub           # Process single file\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s '*.epub'            # Process all EPUB files\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s 'books/*.epub'      # Process EPUB files in books directory\n", os.Args[0])
		os.Exit(1)
	}

	processor := NewEPUBProcessor()

	// Check if input is a single file or pattern
	input := os.Args[1]
	if strings.Contains(input, "*") {
		// Process multiple files
		if err := processor.ProcessMultipleEPUBs(input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Process single file
		if err := processor.ProcessEPUBFile(input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
