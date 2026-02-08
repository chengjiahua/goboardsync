package vision

import (
	"fmt"
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

	// 调用 detector 的批量识别方法
	stats, details, err := BatchRecognizeImages(imagesDir)
	if err != nil {
		t.Fatalf("批量识别失败: %v", err)
	}

	// 打印统计结果
	PrintBatchRecognitionStats(stats, details)

	// 如果需要在测试中验证结果，可以添加断言
	if stats.TotalCount == 0 {
		t.Skip("没有找到测试图像")
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
	Filename  string  `json:"filename"`
	Success   bool    `json:"success"`
	Result    Result  `json:"result"`
	Error     string  `json:"error,omitempty"`
	ExpectedX int     `json:"expected_x"`
	ExpectedY int     `json:"expected_y"`
	ImageSize string  `json:"image_size"`
	Distance  float64 `json:"distance"`
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
			Filename:  filename,
			Success:   success,
			Result:    result,
			ExpectedX: expectedX,
			ExpectedY: expectedY,
			ImageSize: imageSize,
			Distance:  distance,
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
	fmt.Println("\n" + strings.Repeat("-", 104))
	fmt.Printf("%-30s | %-15s | %-15s | %-10s | %-10s | %s\n", "文件名", "预期结果", "检测结果", "图像尺寸", "置信度", "状态")
	fmt.Println(strings.Repeat("-", 104))

	var totalDistance float64
	var maxDistance float64
	var minDistance float64 = math.MaxFloat64

	for _, detail := range details {
		expectedCoord := fmt.Sprintf("%d-%s", detail.Result.Move, detail.Result.Color)
		detectedCoord := fmt.Sprintf("%d-%s", detail.Result.Move, detail.Result.Color)
		if detail.Result.X > 0 && detail.Result.Y > 0 {
			xChar := string(rune('A' + detail.ExpectedX - 1))
			expectedCoord = fmt.Sprintf("%d-%s%d", detail.Result.Move, xChar, detail.ExpectedY)
			detectedXChar := string(rune('A' + detail.Result.X - 1))
			detectedCoord = fmt.Sprintf("%d-%s%d", detail.Result.Move, detectedXChar, detail.Result.Y)
		}

		status := "✅ 正确"
		if !detail.Success {
			status = "❌ 错误"
		}

		fmt.Printf("%-30s | %-15s | %-15s | %-10s | %-10.2f | %s\n",
			detail.Filename, expectedCoord, detectedCoord, detail.ImageSize, detail.Result.Confidence, status)

		if !detail.Success {
			fmt.Printf("   -> 坐标误差: %.2f\n", detail.Distance)
		}

		if detail.Result.X > 0 && detail.Result.Y > 0 {
			totalDistance += detail.Distance * detail.Distance
			if detail.Distance > maxDistance {
				maxDistance = detail.Distance
			}
			if detail.Distance < minDistance {
				minDistance = detail.Distance
			}
		}
	}

	fmt.Println(strings.Repeat("-", 104))
	fmt.Printf("测试总结: 总计 %d, 成功 %d, 失败 %d, 成功率 %.2f%%\n",
		stats.TotalCount, stats.SuccessCount, stats.FailureCount, stats.SuccessRate)
	fmt.Println(strings.Repeat("-", 104))

	if stats.TotalCount > 0 {
		mse := totalDistance / float64(stats.TotalCount)
		rmse := math.Sqrt(mse)

		fmt.Println("误差统计:")
		fmt.Printf("总误差数量: %d\n", stats.TotalCount)
		fmt.Printf("均方误差 (MSE): %.2f\n", mse)
		fmt.Printf("均方根误差 (RMSE): %.2f\n", rmse)
		if maxDistance > 0 {
			fmt.Printf("最大误差: %.2f\n", maxDistance)
		}
		if minDistance < math.MaxFloat64 {
			fmt.Printf("最小误差: %.2f\n", minDistance)
		}
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
