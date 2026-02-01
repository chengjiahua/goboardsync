package vision

import (
	"fmt"
	"image"
	"my-app/board"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocv.io/x/gocv"
)

func TestBatchRecognition(t *testing.T) {
	imagesDir := "../images"
	files, err := os.ReadDir(imagesDir)
	if err != nil {
		t.Fatalf("无法读取目录: %v", err)
	}

	// 初始化基础模型，具体参数会在循环中根据分辨率调整
	b := board.NewBoard(image.Point{}, image.Point{})
	detector := NewDetector(b)

	successCount := 0
	totalCount := 0

	t.Logf("\n%-30s | %-15s | %-15s | %-10s | %-10s", "文件名", "预期(手数-坐标-颜色)", "识别结果", "图片尺寸", "状态")
	t.Log(strings.Repeat("-", 80))

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}

		// 解析文件名: 手数-坐标系统-黑棋/白棋.ext (例如: 123-D4-black.png 或 123-D4-黑棋.png)
		parts := strings.Split(strings.TrimSuffix(name, ext), "-")
		if len(parts) < 3 {
			continue
		}
		totalCount++
		expectHand := parts[0]
		expectGTP := parts[1]
		// 处理颜色：支持 black/white 或 黑棋/白棋，并处理 _1, _2 等后缀
		expectColorRaw := strings.ToLower(strings.Split(parts[2], "_")[0])
		expectColorStr := ""
		if strings.Contains(expectColorRaw, "black") || strings.Contains(expectColorRaw, "黑") {
			expectColorStr = "black"
		} else if strings.Contains(expectColorRaw, "white") || strings.Contains(expectColorRaw, "白") {
			expectColorStr = "white"
		}

		imgPath := filepath.Join(imagesDir, name)
		img := gocv.IMRead(imgPath, gocv.IMReadColor)
		if img.Empty() {
			t.Errorf("[%s] 无法读取图片", name)
			continue
		}

		// 记录图片尺寸以辅助调试 ROI
		imgSize := fmt.Sprintf("%dx%d", img.Cols(), img.Rows())

		// 自动校准棋盘区域
		hGrid, vGrid, err := detector.AutoCalibrateBoard(img)
		if err != nil {
			t.Logf("[%s] 自动校准失败: %v, 使用硬编码备选方案", name, err)
			detector.HGrid = nil
			detector.VGrid = nil
			// 根据分辨率动态调整 ROI (备选方案)
			if img.Cols() == 1200 && img.Rows() == 2670 {
				detector.BoardModel.TopLeft = image.Point{X: 28, Y: 512}
				detector.BoardModel.BottomRight = image.Point{X: 1180, Y: 1664}
			} else if img.Cols() == 1125 && img.Rows() == 2436 {
				detector.BoardModel.TopLeft = image.Point{X: 25, Y: 590}
				detector.BoardModel.BottomRight = image.Point{X: 1195, Y: 1690}
			}
		} else {
			detector.HGrid = hGrid
			detector.VGrid = vGrid
			detector.BoardModel.TopLeft = image.Point{X: vGrid[0], Y: hGrid[0]}
			detector.BoardModel.BottomRight = image.Point{X: vGrid[18], Y: hGrid[18]}
			t.Logf("[%s] 自动校准成功: Grid 19x19, TL=%v, BR=%v", name, detector.BoardModel.TopLeft, detector.BoardModel.BottomRight)
		}

		detector.BoardModel.GridWidth = float64(detector.BoardModel.BottomRight.X-detector.BoardModel.TopLeft.X) / 18.0
		detector.BoardModel.GridHeight = float64(detector.BoardModel.BottomRight.Y-detector.BoardModel.TopLeft.Y) / 18.0

		row, col, color, hand := detector.DetectLatestMove(img)
		img.Close()

		actualGTP := "None"
		if row != -1 && col != -1 {
			actualGTP = board.ConvertToGTP(row, col)
		}
		actualColorStr := "None"
		if color == ColorBlack {
			actualColorStr = "black"
		} else if color == ColorWhite {
			actualColorStr = "white"
		}

		expectStr := fmt.Sprintf("%s-%s-%s", expectHand, expectGTP, expectColorStr)
		actualStr := fmt.Sprintf("%s-%s-%s", hand, actualGTP, actualColorStr)

		isCorrect := hand == expectHand && actualGTP == expectGTP && actualColorStr == expectColorStr
		status := "✅ 正确"
		if !isCorrect {
			status = "❌ 错误"
			t.Logf("[%s] 识别错误: 预期 %s, 实际 %s (尺寸: %s)", name, expectStr, actualStr, imgSize)
		} else {
			successCount++
		}

		t.Logf("%-30s | %-15s | %-15s | %-10s | %s", name, expectStr, actualStr, imgSize, status)
	}

	t.Log(strings.Repeat("-", 80))
	t.Logf("测试总结: 总计 %d, 成功 %d, 失败 %d, 成功率 %.2f%%",
		totalCount, successCount, totalCount-successCount, float64(successCount)/float64(totalCount)*100)
}
