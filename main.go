package main

import (
	"bufio"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/nfnt/resize"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/image/font"
)

const maxPixels = 12000000 // 12 Megapixels
const batchSize = 200      // Number of files to process in each batch

func humanReadableSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", size)
	}
}

func calculateTotalSizeAndCount(folderPath, outputFolder string) (int, int64, []string, error) {
	var totalFiles int
	var totalSize int64
	var filePaths []string

	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && (filepath.Base(path) == "compressed_files" || path == outputFolder) {
			return filepath.SkipDir
		}

		if !info.IsDir() && (strings.HasSuffix(strings.ToLower(info.Name()), ".jpg") || strings.HasSuffix(strings.ToLower(info.Name()), ".png")) {
			compressedFilePath := filepath.Join(outputFolder, strings.TrimPrefix(path, folderPath))
			compressedFilePath = filepath.Join(filepath.Dir(compressedFilePath), info.Name())
			if _, err := os.Stat(compressedFilePath); os.IsNotExist(err) {
				totalFiles++
				totalSize += info.Size()
				filePaths = append(filePaths, path)
			}
		}

		return nil
	})

	if err != nil {
		return 0, 0, nil, fmt.Errorf("failed to walk the directory: %v", err)
	}

	return totalFiles, totalSize, filePaths, nil
}

func addWatermark(img image.Image, text string, fontPath string) (image.Image, error) {
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)

	fontBytes, err := ioutil.ReadFile(fontPath)
	if err != nil {
		return nil, err
	}

	fnt, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return nil, err
	}

	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(fnt)
	c.SetFontSize(20)
	c.SetClip(rgba.Bounds())
	c.SetDst(rgba)
	c.SetSrc(image.Black)
	c.SetHinting(font.HintingNone)

	// Measure the text dimensions
	face := truetype.NewFace(fnt, &truetype.Options{Size: 20, DPI: 72})
	d := &font.Drawer{
		Face: face,
	}
	textBounds, _ := d.BoundString(text)
	textWidth := (textBounds.Max.X - textBounds.Min.X).Ceil()
	textHeight := (textBounds.Max.Y - textBounds.Min.Y).Ceil()

	pt := freetype.Pt(rgba.Bounds().Dx()-textWidth-10, rgba.Bounds().Dy()-textHeight+int(c.PointToFixed(20)>>6)-10)

	_, err = c.DrawString(text, pt)
	if err != nil {
		return nil, err
	}

	return rgba, nil
}

func compressImage(inputPath, outputPath string, maxPixels int, watermarkText, fontPath string) error {
	file, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open image: %v", err)
	}
	defer file.Close()

	img, format, err := image.Decode(file)
	if err != nil {
		return fmt.Errorf("failed to decode image: %v", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	totalPixels := width * height

	var newImg image.Image
	if totalPixels > maxPixels {
		scaleFactor := float64(maxPixels) / float64(totalPixels)
		newWidth := uint(float64(width) * scaleFactor)
		newHeight := uint(float64(height) * scaleFactor)
		newImg = resize.Resize(newWidth, newHeight, img, resize.Lanczos3)
	} else {
		newImg = img
	}

	if watermarkText != "" {
		// Add watermark
		newImg, err = addWatermark(newImg, watermarkText, fontPath)
		if err != nil {
			return fmt.Errorf("failed to add watermark: %v", err)
		}
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	switch format {
	case "jpeg":
		err = jpeg.Encode(outFile, newImg, &jpeg.Options{Quality: 80})
	case "png":
		err = png.Encode(outFile, newImg)
	default:
		return fmt.Errorf("unsupported image format: %s", format)
	}

	if err != nil {
		return fmt.Errorf("failed to encode image: %v", err)
	}

	return nil
}

func compressImages(threadID int, files []string, outputDir, inputDir, watermarkText, fontPath string, maxPixels int, bar *progressbar.ProgressBar, failedFiles *[]string, mu *sync.Mutex) (int64, error) {
	fmt.Printf("Thread %d starting to compress %d images.\n", threadID, len(files))

	var compressedTotalSize int64
	filesPerBatch := batchSize
	if len(files) < batchSize {
		filesPerBatch = len(files)
	}

	for i := 0; i < len(files); i += filesPerBatch {
		end := i + filesPerBatch
		if end > len(files) {
			end = len(files)
		}
		batch := files[i:end]
		fmt.Printf("Thread %d processing batch of %d files.\n", threadID, len(batch))
		for _, path := range batch {
			if info, err := os.Stat(path); err == nil {
				if !info.IsDir() && (strings.HasSuffix(strings.ToLower(info.Name()), ".jpg") || strings.HasSuffix(strings.ToLower(info.Name()), ".png")) {
					relativePath := strings.TrimPrefix(path, inputDir)
					outputFile := filepath.Join(outputDir, relativePath)

					// Create the necessary directories
					os.MkdirAll(filepath.Dir(outputFile), os.ModePerm)

					if err := compressImage(path, outputFile, maxPixels, watermarkText, fontPath); err == nil {
						bar.Add(1)
						if info, err := os.Stat(outputFile); err == nil {
							compressedTotalSize += info.Size()
						}
					} else {
						fmt.Printf("Thread %d failed to compress file %s: %v\n", threadID, path, err)
						mu.Lock()
						*failedFiles = append(*failedFiles, relativePath)
						mu.Unlock()
					}
				}
			} else {
				fmt.Printf("Thread %d failed to stat file %s: %v\n", threadID, path, err)
				mu.Lock()
				*failedFiles = append(*failedFiles, strings.TrimPrefix(path, inputDir))
				mu.Unlock()
			}
		}
	}

	fmt.Printf("Thread %d finished compressing %d images.\n", threadID, len(files))
	return compressedTotalSize, nil
}

func getConfirmation() bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Do you want to proceed? (Y/N): ")
	ch := make(chan string, 1)
	go func() {
		text, _ := reader.ReadString('\n')
		ch <- strings.TrimSpace(strings.ToLower(text))
	}()

	select {
	case res := <-ch:
		return res == "y"
	case <-time.After(10 * time.Second):
		fmt.Println("\nNo input received, defaulting to 'No'")
		return false
	}
}

func writeReport(reportFile string, startTime time.Time, maxPixels, numThreads int, outputDir, watermarkText, fontPath string, skipConfirmation bool, totalFiles int, totalSize, compressedSize int64, endTime time.Time, failedFiles []string) error {
	file, err := os.Create(reportFile)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	fmt.Fprintf(writer, "Start Time: %s\n", startTime.Format(time.RFC1123))
	fmt.Fprintf(writer, "Max Pixels: %d\n", maxPixels)
	fmt.Fprintf(writer, "Number of Threads: %d\n", numThreads)
	fmt.Fprintf(writer, "Output Directory: %s\n", outputDir)
	fmt.Fprintf(writer, "Watermark Text: %s\n", watermarkText)
	fmt.Fprintf(writer, "Font Path: %s\n", fontPath)
	fmt.Fprintf(writer, "Skip Confirmation: %v\n", skipConfirmation)
	fmt.Fprintf(writer, "Total Files: %d\n", totalFiles)
	fmt.Fprintf(writer, "Total Size Before Compression: %s\n", humanReadableSize(totalSize))
	fmt.Fprintf(writer, "Total Size After Compression: %s\n", humanReadableSize(compressedSize))
	fmt.Fprintf(writer, "End Time: %s\n", endTime.Format(time.RFC1123))
	fmt.Fprintf(writer, "Total Time Taken: %s\n", endTime.Sub(startTime))
	fmt.Fprintf(writer, "Failed Files Count: %d\n", len(failedFiles))
	fmt.Fprintf(writer, "Failed Files:\n")
	for _, file := range failedFiles {
		fmt.Fprintf(writer, "%s\n", file)
	}

	return writer.Flush()
}

func main() {
	var maxPixels, numThreads int
	var outputDir, watermarkText, fontPath string
	var skipConfirmation bool
	flag.IntVar(&maxPixels, "s", 12000000, "maximum number of pixels for the resized image")
	flag.IntVar(&numThreads, "t", 10, "number of threads")
	flag.StringVar(&outputDir, "d", "", "directory to save compressed images")
	flag.StringVar(&watermarkText, "w", "", "watermark text")
	flag.StringVar(&fontPath, "f", "InkType.ttf", "path to the font file")
	flag.BoolVar(&skipConfirmation, "y", false, "skip confirmation")
	flag.Parse()

	if len(flag.Args()) < 1 {
		fmt.Println("Usage: image-compressor -s <maxPixels> -t <numThreads> -d <outputDir> -w <watermarkText> -f <fontPath> -y <path>")
		return
	}

	inputPath := flag.Arg(0)
	info, err := os.Stat(inputPath)
	if err != nil {
		fmt.Printf("Error accessing the path: %v\n", err)
		return
	}

	if outputDir == "" {
		outputDir = inputPath
	}

	compressedFolder := filepath.Join(outputDir, "compressed_files")
	err = os.MkdirAll(compressedFolder, 0755)
	if err != nil {
		fmt.Printf("Failed to create compressed_files folder: %v\n", err)
		return
	}

	var totalFiles int
	var totalSize int64
	var filePaths []string

	if info.IsDir() {
		totalFiles, totalSize, filePaths, err = calculateTotalSizeAndCount(inputPath, compressedFolder)
	} else {
		totalFiles = 1
		totalSize = info.Size()
		filePaths = []string{inputPath}
	}

	approxSize := int64(float64(totalSize) * 0.5) // Approximate size after compression (50% of original)

	fmt.Printf("Total files to be compressed: %d\n", totalFiles)
	fmt.Printf("Total size of current files: %s\n", humanReadableSize(totalSize))
	fmt.Printf("Approximate size after conversion: %s\n", humanReadableSize(approxSize))

	// Estimate time required (assuming each file takes 0.5 seconds to compress)
	estimatedTime := time.Duration(totalFiles) * 500 * time.Millisecond
	fmt.Printf("Estimated time required: %v\n", estimatedTime)

	// Ask for confirmation if the -y flag is not provided
	if !skipConfirmation {
		if !getConfirmation() {
			fmt.Println("Operation cancelled.")
			return
		}
	}

	// Start the compression and measure the actual time taken
	startTime := time.Now()

	// Create a progress bar for each thread
	bars := make([]*progressbar.ProgressBar, numThreads)
	for i := range bars {
		bars[i] = progressbar.NewOptions(len(filePaths)/numThreads+1, progressbar.OptionSetDescription(fmt.Sprintf("Thread %d", i+1)))
	}

	// Divide files among threads
	var wg sync.WaitGroup
	var compressedTotalSize int64
	var failedFiles []string
	var mu sync.Mutex
	chunkSize := (len(filePaths) + numThreads - 1) / numThreads
	for i := 0; i < numThreads; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(filePaths) {
			end = len(filePaths)
		}
		if start < end {
			wg.Add(1)
			go func(threadID int, files []string, bar *progressbar.ProgressBar) {
				defer wg.Done()
				size, _ := compressImages(threadID, files, compressedFolder, inputPath, watermarkText, fontPath, maxPixels, bar, &failedFiles, &mu)
				mu.Lock()
				compressedTotalSize += size
				mu.Unlock()
			}(i+1, filePaths[start:end], bars[i])
		}
	}

	wg.Wait()

	endTime := time.Now()
	actualTimeTaken := endTime.Sub(startTime)
	fmt.Printf("\nActual time taken: %v\n", actualTimeTaken)

	if err := writeReport(filepath.Join(compressedFolder, "report.txt"), startTime, maxPixels, numThreads, outputDir, watermarkText, fontPath, skipConfirmation, totalFiles, totalSize, compressedTotalSize, endTime, failedFiles); err != nil {
		fmt.Printf("Error writing report: %v\n", err)
	} else {
		fmt.Println("Compression completed successfully")
	}
}
