package vision

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gocv.io/x/gocv"
)

func TestBatchRecognition(t *testing.T) {
	imagesDir := "../images"
	debugBaseDir := "debug"
	os.RemoveAll(debugBaseDir)

	files, _ := os.ReadDir(imagesDir)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jpg") {
			continue
		}

		filename := file.Name()
		caseDir := filepath.Join(debugBaseDir, strings.TrimSuffix(filename, filepath.Ext(filename)))
		os.MkdirAll(caseDir, 0755)

		img := gocv.IMRead(filepath.Join(imagesDir, filename), gocv.IMReadColor)
		if img.Empty() {
			continue
		}
		defer img.Close()

		moveNum, _, expX, expY, _ := parseFilename(filename)

		corners := FixedBoardCorners["1200x2670"]
		warped, _ := WarpBoard(img, corners)
		defer warped.Close()

		result, _ := DetectLastMoveCoord(img, moveNum)

		drawGrid(warped)

		gocv.Rectangle(&warped, result.MarkerRect, colorToScalar("yellow"), 2)

		gocv.Circle(&warped, result.MarkerRect.Min, 5, colorToScalar("green"), -1)

		cellW := float64(warped.Cols()) / 19.0
		cellH := float64(warped.Rows()) / 19.0
		centerPt := image.Pt(
			int(float64(result.MarkerRect.Min.X)+cellW/2.0),
			int(float64(result.MarkerRect.Min.Y)+cellH/2.0),
		)
		gocv.Circle(&warped, centerPt, 8, colorToScalar("red"), 2)

		info := fmt.Sprintf("Exp: %c%d, Got: %c%d", 'A'+expX-1, expY, 'A'+result.X-1, result.Y)
		gocv.PutText(&warped, info, image.Pt(20, 50), gocv.FontHersheySimplex, 1.2, colorToScalar("purple"), 3)

		debugPath := filepath.Join(caseDir, "debug_warped.jpg")
		gocv.IMWrite(debugPath, warped)

		if result.X != expX || result.Y != expY {
			fmt.Printf("错误样本已记录: %s\n", filename)
		}
	}
}

func TestPrintBatchStats(t *testing.T) {
	imagesDir := "../images"
	stats, details, err := BatchRecognizeImages(imagesDir)
	if err != nil {
		t.Fatalf("批量识别失败: %v", err)
	}

	PrintBatchRecognitionStats(stats, details)
}

func drawGrid(img gocv.Mat) {
	w, h := img.Cols(), img.Rows()
	stepW, stepH := float64(w)/19.0, float64(h)/19.0
	gray := colorToScalar("gray")

	for i := 0; i < 19; i++ {
		y := int(float64(i)*stepH + stepH/2)
		gocv.Line(&img, image.Pt(0, y), image.Pt(w, y), gray, 1)
		x := int(float64(i)*stepW + stepW/2)
		gocv.Line(&img, image.Pt(x, 0), image.Pt(x, h), gray, 1)
	}
}

func colorToScalar(name string) color.RGBA {
	switch name {
	case "red":
		return color.RGBA{0, 0, 255, 0}
	case "green":
		return color.RGBA{0, 255, 0, 0}
	case "yellow":
		return color.RGBA{0, 255, 255, 0}
	case "purple":
		return color.RGBA{255, 0, 255, 0}
	case "gray":
		return color.RGBA{200, 200, 200, 0}
	default:
		return color.RGBA{255, 255, 255, 0}
	}
}

// BatchStats 批量识别统计信息
type BatchStats struct {
	TotalCount   int     `json:"total_count"`
	SuccessCount int     `json:"success_count"`
	FailureCount int     `json:"failure_count"`
	SuccessRate  float64 `json:"success_rate"`
	BlackCount   int     `json:"black_count"`
	WhiteCount   int     `json:"white_count"`
}

// BatchDetail 批量识别详细信息
type BatchDetail struct {
	Filename      string  `json:"filename"`
	Success       bool    `json:"success"`
	Result        Result  `json:"result"`
	Error         string  `json:"error,omitempty"`
	ExpectedX     int     `json:"expected_x"`
	ExpectedY     int     `json:"expected_y"`
	ExpectedMove  int     `json:"expected_move"`
	ExpectedColor string  `json:"expected_color"`
	ImageSize     string  `json:"image_size"`
	Distance      float64 `json:"distance"`
}

// BatchRecognizeImages 批量识别图像
func BatchRecognizeImages(imagesDir string) (*BatchStats, []BatchDetail, error) {
	var stats BatchStats
	var details []BatchDetail

	files, err := os.ReadDir(imagesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("读取图像目录失败: %v", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filename := file.Name()
		imagePath := filepath.Join(imagesDir, filename)

		if !strings.HasSuffix(strings.ToLower(filename), ".jpg") &&
			!strings.HasSuffix(strings.ToLower(filename), ".png") {
			continue
		}

		moveNumber, color, expectedX, expectedY, err := parseFilename(filename)
		if err != nil {
			details = append(details, BatchDetail{
				Filename: filename,
				Success:  false,
				Error:    fmt.Sprintf("解析文件名失败: %v", err),
			})
			continue
		}

		img := gocv.IMRead(imagePath, gocv.IMReadColor)
		if img.Empty() {
			details = append(details, BatchDetail{
				Filename: filename,
				Success:  false,
				Error:    "读取图像失败",
			})
			continue
		}
		defer img.Close()

		imageSize := fmt.Sprintf("%dx%d", img.Cols(), img.Rows())

		result, err := DetectLastMoveCoord(img, moveNumber)
		if err != nil {
			details = append(details, BatchDetail{
				Filename: filename,
				Success:  false,
				Error:    fmt.Sprintf("检测失败: %v", err),
			})
			continue
		}

		distance := math.Sqrt(math.Pow(float64(result.X-expectedX), 2) + math.Pow(float64(result.Y-expectedY), 2))
		success := result.X > 0 && result.Y > 0 && result.Color == color && distance < 0.5

		details = append(details, BatchDetail{
			Filename:      filename,
			Success:       success,
			Result:        result,
			ExpectedX:     expectedX,
			ExpectedY:     expectedY,
			ExpectedMove:  moveNumber,
			ExpectedColor: color,
			ImageSize:     imageSize,
			Distance:      distance,
		})

		stats.TotalCount++
		if success {
			stats.SuccessCount++
			if color == "B" {
				stats.BlackCount++
			} else {
				stats.WhiteCount++
			}
		} else {
			stats.FailureCount++
		}
	}

	if stats.TotalCount > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalCount) * 100
	}

	return &stats, details, nil
}

// PrintBatchRecognitionStats 打印批量识别统计结果
func PrintBatchRecognitionStats(stats *BatchStats, details []BatchDetail) {
	fmt.Println("\n" + strings.Repeat("-", 105))
	fmt.Printf("%-30s | %-18s | %-18s | %-10s | %-10s | %s\n", "文件名", "预期结果", "检测结果", "图像尺寸", "置信度", "状态")
	fmt.Println(strings.Repeat("-", 105))

	var totalDistance float64
	var maxDistance float64
	var minDistance float64 = math.MaxFloat64
	var errorCount int

	for _, detail := range details {
		// 预期结果格式: {move}-{coord}-{color} 如: 59-F15-black
		expectedCoord := fmt.Sprintf("X0-black")
		if detail.ExpectedX > 0 && detail.ExpectedY > 0 {
			colorChar := "black"
			if strings.ToLower(detail.ExpectedColor) == "w" {
				colorChar = "white"
			}
			xChar := string(rune('A' + detail.ExpectedX - 1))
			expectedCoord = fmt.Sprintf("%d-%s%d-%s", detail.ExpectedMove, xChar, detail.ExpectedY, colorChar)
		}

		// 检测结果格式: {move}-{coord}-{color} 如: 59-F15-black
		detectedCoord := fmt.Sprintf("X0-black")
		if detail.Result.X > 0 && detail.Result.Y > 0 {
			colorChar := "black"
			if strings.ToLower(detail.Result.Color) == "w" {
				colorChar = "white"
			}
			xChar := string(rune('A' + detail.Result.X - 1))
			detectedCoord = fmt.Sprintf("%d-%s%d-%s", detail.Result.Move, xChar, detail.Result.Y, colorChar)
		}

		status := "✅ 正确"
		if !detail.Success {
			status = "❌ 错误"
		}

		fmt.Printf("%-30s | %-18s | %-18s | %-10s | %-10.2f | %s\n",
			detail.Filename, expectedCoord, detectedCoord, detail.ImageSize, detail.Result.Confidence, status)

		if !detail.Success {
			fmt.Printf("   -> 坐标误差: %.2f\n", detail.Distance)
		}

		if detail.Distance > 0 {
			errorCount++
			totalDistance += detail.Distance * detail.Distance
			if detail.Distance > maxDistance {
				maxDistance = detail.Distance
			}
			if detail.Distance < minDistance {
				minDistance = detail.Distance
			}
		}
	}

	fmt.Println(strings.Repeat("-", 105))
	fmt.Printf("测试总结: 总计 %d, 成功 %d, 失败 %d, 成功率 %.2f%%\n",
		stats.TotalCount, stats.SuccessCount, stats.FailureCount, stats.SuccessRate)
	fmt.Println(strings.Repeat("-", 105))

	if errorCount > 0 {
		mse := totalDistance / float64(errorCount)
		rmse := math.Sqrt(mse)

		fmt.Println("误差统计:")
		fmt.Printf("总误差数量: %d\n", errorCount)
		fmt.Printf("均方误差 (MSE): %.2f\n", mse)
		fmt.Printf("均方根误差 (RMSE): %.2f\n", rmse)
		fmt.Printf("最大误差: %.2f\n", maxDistance)
		fmt.Printf("最小误差: %.2f\n", minDistance)
	}
}

// parseFilename 从文件名解析手数、颜色和预期坐标
// 文件名格式: {move}-{coord}-{color}.jpg 或 {move}-{coord}-{color}.png
// 例如: 1-P4-black.jpg, 2-Q5-white.png
func parseFilename(filename string) (int, string, int, int, error) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))

	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		return 0, "", 0, 0, fmt.Errorf("文件名格式不正确: %s", filename)
	}

	moveNumber, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", 0, 0, fmt.Errorf("手数解析失败: %v", err)
	}

	color := strings.ToUpper(string(parts[2][0]))
	if color != "B" && color != "W" {
		return 0, "", 0, 0, fmt.Errorf("颜色不正确: %s", parts[2])
	}

	coord := parts[1]
	if len(coord) < 2 {
		return 0, "", 0, 0, fmt.Errorf("坐标格式不正确: %s", coord)
	}

	coordX := int(coord[0] - 'A' + 1)
	coordY, err := strconv.Atoi(coord[1:])
	if err != nil {
		return 0, "", 0, 0, fmt.Errorf("坐标Y解析失败: %v", err)
	}

	return moveNumber, color, coordX, coordY, nil
}
