package vision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gocv.io/x/gocv"
)

const (
	// BoardWarpSize 棋盘矫正后的大小
	BoardWarpSize = 1024
)

// FixedBoardCorners 为常见分辨率预定义的棋盘四角（按顺时针或逆时针顺序）
var FixedBoardCorners = map[string][]image.Point{
	"1200x2670": {
		{40, 536},
		{1160, 536},
		{1160, 1650},
		{40, 1650},
	},
}

// Result 识别结果结构
type Result struct {
	Move       int            `json:"move"`
	Color      string         `json:"color"` // "W" or "B"
	X          int            `json:"x"`     // 1..19
	Y          int            `json:"y"`     // 1..19
	Confidence float64        `json:"confidence"`
	Debug      map[string]any `json:"debug"`
}

// Detector 检测器结构体
type Detector struct {
	OCREndpoint string // OCR 服务地址
}

// NewDetector 创建新的检测器
func NewDetector() *Detector {
	return &Detector{
		OCREndpoint: "http://127.0.0.1:5001/ocr",
	}
}

// FetchMoveNumberFromOCR 调用本地 OCR 接口获取当前手数
func (d *Detector) FetchMoveNumberFromOCR(img gocv.Mat) (int, error) {
	if img.Empty() {
		return 0, fmt.Errorf("图片为空")
	}

	// 1. 将 gocv.Mat 编码为 jpeg
	buf := new(bytes.Buffer)
	imgBytes, err := gocv.IMEncode(".jpg", img)
	if err != nil {
		return 0, fmt.Errorf("编码图片失败: %v", err)
	}
	defer imgBytes.Close()
	buf.Write(imgBytes.GetBytes())

	// 2. 构建 multipart/form-data 请求
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "image.jpg")
	if err != nil {
		return 0, fmt.Errorf("创建表单文件失败: %v", err)
	}
	io.Copy(part, buf)
	writer.Close()

	// 3. 发送 POST 请求
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", d.OCREndpoint, body)
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("OCR 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("OCR 响应错误: %d", resp.StatusCode)
	}

	// 4. 解析响应
	var results []struct {
		Words string `json:"words"`
	}
	respData, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(respData, &results)
	if err != nil {
		// 尝试另一种格式 (有些 OCR 返回的是对象列表)
		var wrapper struct {
			Results []struct {
				Words string `json:"words"`
			} `json:"results"`
		}
		if err2 := json.Unmarshal(respData, &wrapper); err2 == nil {
			results = wrapper.Results
		} else {
			return 0, fmt.Errorf("解析 OCR 结果失败: %v", err)
		}
	}

	// 5. 正则提取手数
	re := regexp.MustCompile(`第\s*(\d+)\s*手`)
	for _, result := range results {
		matches := re.FindStringSubmatch(result.Words)
		if len(matches) > 1 {
			moveNum, err := strconv.Atoi(matches[1])
			if err == nil && moveNum > 0 {
				return moveNum, nil
			}
		}
	}

	return 0, fmt.Errorf("未识别到有效手数")
}

// WarpBoard 对棋盘进行透视矫正
func WarpBoard(img gocv.Mat, corners []image.Point) (gocv.Mat, error) {
	// 确保有4个角点
	if len(corners) != 4 {
		return gocv.Mat{}, fmt.Errorf("需要4个角点")
	}

	// 目标棋盘大小
	dst := []image.Point{
		{0, 0},
		{BoardWarpSize, 0},
		{BoardWarpSize, BoardWarpSize},
		{0, BoardWarpSize},
	}

	// 转换为 gocv.PointVector
	srcPoints := gocv.NewPointVectorFromPoints(corners)
	defer srcPoints.Close()

	dstPoints := gocv.NewPointVectorFromPoints(dst)
	defer dstPoints.Close()

	// 计算透视变换矩阵
	M := gocv.GetPerspectiveTransform(srcPoints, dstPoints)
	if M.Empty() {
		return gocv.Mat{}, fmt.Errorf("计算透视变换矩阵失败")
	}

	// 应用透视变换
	warped := gocv.NewMat()
	gocv.WarpPerspective(img, &warped, M, image.Point{X: BoardWarpSize, Y: BoardWarpSize})

	return warped, nil
}

// DetectLastMoveCoord 检测最后一手的坐标
func DetectLastMoveCoord(img gocv.Mat, moveNumber int) (Result, error) {
	// 初始化详细的调试信息
	debugInfo := make(map[string]any)
	debugInfo["image_size"] = fmt.Sprintf("%dx%d", img.Cols(), img.Rows())
	debugInfo["move_number"] = moveNumber

	// 声明corners变量
	var corners []image.Point

	// 1. 棋盘定位与矫正
	debugInfo["step"] = "board_localization"

	// 使用固定的棋盘位置，基于用户提供的截图
	debugInfo["board_localization_method"] = "fixed"

	// 使用全局预定义的硬编码棋盘区域，保证调试输出与实际使用一致
	resKey := fmt.Sprintf("%dx%d", img.Cols(), img.Rows())
	if c, ok := FixedBoardCorners[resKey]; ok {
		corners = c
		debugInfo["fixed_resolution"] = resKey
	} else {
		return Result{
			Move:       moveNumber,
			Color:      "B",
			X:          0,
			Y:          0,
			Confidence: 0,
			Debug:      debugInfo,
		}, fmt.Errorf("不支持的图片分辨率: %dx%d，请添加硬编码的棋盘区域", img.Cols(), img.Rows())
	}

	// 2. 透视矫正
	warped, err := WarpBoard(img, corners)
	if err != nil {
		debugInfo["warp_error"] = err.Error()
		debugInfo["final_status"] = "failed_at_warp"
		// 透视矫正失败，返回默认结果
		return Result{
			Move:       moveNumber,
			Color:      "B",
			X:          0,
			Y:          0,
			Confidence: 0,
			Debug:      debugInfo,
		}, nil
	}
	defer warped.Close()

	// 3. 识别黑白棋
	var color string
	var gridX, gridY int

	isBlack := moveNumber%2 == 1
	if isBlack {
		// 识别黑棋
		gridX, gridY, err = boardblack(warped)
		color = "B"
	} else {
		// 识别白棋
		gridX, gridY, err = boardwhite(warped)
		color = "W"
	}

	if err != nil {
		debugInfo["detection_error"] = err.Error()
		debugInfo["final_status"] = "failed_at_detection"
		return Result{
			Move:       moveNumber,
			Color:      color,
			X:          0,
			Y:          0,
			Confidence: 0,
			Debug:      debugInfo,
		}, nil
	}

	// 4. 构建结果
	debugInfo["final_status"] = "success"
	result := Result{
		Move:       moveNumber,
		Color:      color,
		X:          gridX + 1, // 转换为 1-based
		Y:          gridY + 1, // 转换为 1-based
		Confidence: 0.8,       // 固定置信度
		Debug:      debugInfo,
	}

	return result, nil
}

// boardblack 识别黑棋
func boardblack(img gocv.Mat) (int, int, error) {
	// 2. 转换到 HSV 颜色空间
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// 定义红色范围 (HSV)
	// 红色在 HSV 中分布在两端，需要两个范围
	lower1 := gocv.NewScalar(0, 150, 150, 0)
	upper1 := gocv.NewScalar(10, 255, 255, 0)
	lower2 := gocv.NewScalar(160, 150, 150, 0)
	upper2 := gocv.NewScalar(180, 255, 255, 0)

	// 创建用于 InRange 的边界 Mat
	l1 := gocv.NewMatWithSizeFromScalar(lower1, hsv.Rows(), hsv.Cols(), hsv.Type())
	u1 := gocv.NewMatWithSizeFromScalar(upper1, hsv.Rows(), hsv.Cols(), hsv.Type())
	l2 := gocv.NewMatWithSizeFromScalar(lower2, hsv.Rows(), hsv.Cols(), hsv.Type())
	u2 := gocv.NewMatWithSizeFromScalar(upper2, hsv.Rows(), hsv.Cols(), hsv.Type())
	defer l1.Close()
	defer u1.Close()
	defer l2.Close()
	defer u2.Close()

	mask1 := gocv.NewMat()
	mask2 := gocv.NewMat()
	mask := gocv.NewMat()
	defer mask1.Close()
	defer mask2.Close()
	defer mask.Close()

	// 过滤颜色
	gocv.InRange(hsv, l1, u1, &mask1)
	gocv.InRange(hsv, l2, u2, &mask2)
	gocv.BitwiseOr(mask1, mask2, &mask)

	// 3. 寻找红色区域的轮廓
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return 0, 0, fmt.Errorf("未找到红色最后一手标记")
	}

	// 找到最大的红色区域（角标）
	var maxArea float64
	var bestRect image.Rectangle
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		if area > maxArea {
			maxArea = area
			bestRect = gocv.BoundingRect(contours.At(i))
		}
	}

	// 计算红色角标的中心位置
	indicatorX := float64(bestRect.Min.X+bestRect.Max.X) / 2.0
	indicatorY := float64(bestRect.Min.Y+bestRect.Max.Y) / 2.0

	// 4. 将像素坐标映射到 19x19 网格
	rows, cols := img.Rows(), img.Cols()

	// 棋盘格子的平均宽度和高度
	cellW := float64(cols) / 19.0
	cellH := float64(rows) / 19.0

	// 修正偏移：红色角标位于黑棋左上角，我们加上半个格子的偏移以对准棋子中心
	gridX := int(math.Floor(indicatorX / cellW))
	gridY := int(math.Floor(indicatorY / cellH))

	// 越界检查
	if gridX >= 0 && gridX < 19 && gridY >= 0 && gridY < 19 {
		return gridX, gridY, nil
	} else {
		return 0, 0, fmt.Errorf("计算出的坐标超出范围: X:%d, Y:%d", gridX, gridY)
	}
}

// boardwhite 识别白棋
func boardwhite(img gocv.Mat) (int, int, error) {
	// 2. 转换到 HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// 3. 识别蓝色角标 (白棋最后一手)
	lowerBlue := gocv.NewScalar(100, 120, 100, 0)
	upperBlue := gocv.NewScalar(140, 255, 255, 0)

	mask := gocv.NewMat()
	defer mask.Close()
	l := gocv.NewMatWithSizeFromScalar(lowerBlue, hsv.Rows(), hsv.Cols(), hsv.Type())
	u := gocv.NewMatWithSizeFromScalar(upperBlue, hsv.Rows(), hsv.Cols(), hsv.Type())
	defer l.Close()
	defer u.Close()

	gocv.InRange(hsv, l, u, &mask)

	// 4. 寻找轮廓
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return 0, 0, fmt.Errorf("未检测到蓝色角标")
	}

	var maxArea float64
	var bestRect image.Rectangle
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		if area > maxArea {
			maxArea = area
			bestRect = gocv.BoundingRect(contours.At(i))
		}
	}

	// 5. 坐标计算逻辑优化
	rows, cols := float64(img.Rows()), float64(img.Cols())

	// 角标在左上角，我们取它的中心点
	markerX := float64(bestRect.Min.X+bestRect.Max.X) / 2.0
	markerY := float64(bestRect.Min.Y+bestRect.Max.Y) / 2.0

	// 关键修正：
	// 棋子中心大约在角标右下方 1/3 个格子处。
	// 我们估算出棋子中心的像素位置，然后再映射到 0-18 的网格线上。
	cellW := cols / 19.0
	cellH := rows / 19.0

	// 补偿角标到棋子中心的位移（向右下偏移约 0.4 个格子）
	estimatedCenterX := markerX + (cellW * 0.4)
	estimatedCenterY := markerY + (cellH * 0.4)

	// 使用 Round（四舍五入）找到最接近的第几条线
	// 由于 19 条线对应 18 个间隔，我们用总宽除以 19 区间来定位
	gridX := int(math.Floor(estimatedCenterX / cellW))
	gridY := int(math.Floor(estimatedCenterY / cellH))

	// 强制限定在 0-18 范围内
	gridX = clamp(gridX, 0, 18)
	gridY = clamp(gridY, 0, 18)

	return gridX, gridY, nil
}

// clamp 保证索引在 0-18 之间
func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
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

		err = saveDebugInfo(imagesDir, filename, result, img)
		if err != nil {
			fmt.Printf("保存 debug 信息失败 %s: %v\n", filename, err)
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

// saveDebugInfo 保存 debug 信息和图像
func saveDebugInfo(imagesDir, filename string, result Result, img gocv.Mat) error {
	debugDir := filepath.Join(imagesDir, "debug")
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("创建 debug 目录失败: %v", err)
	}

	testDir := filepath.Join(debugDir, strings.TrimSuffix(filename, filepath.Ext(filename)))
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return fmt.Errorf("创建测试用例 debug 目录失败: %v", err)
	}

	originalPath := filepath.Join(testDir, "original.jpg")
	gocv.IMWrite(originalPath, img)

	debugPath := filepath.Join(testDir, "debug.json")
	debugData := map[string]any{
		"filename":    filename,
		"move_number": result.Move,
		"color":       result.Color,
		"x":           result.X,
		"y":           result.Y,
		"confidence":  result.Confidence,
		"debug":       result.Debug,
	}

	jsonData, err := json.MarshalIndent(debugData, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 debug 信息失败: %v", err)
	}

	if err := os.WriteFile(debugPath, jsonData, 0644); err != nil {
		return fmt.Errorf("保存 debug 信息失败: %v", err)
	}

	if result.X > 0 && result.Y > 0 {
		markedImg := img.Clone()
		defer markedImg.Close()

		centerX := (result.X-1)*img.Cols()/19 + img.Cols()/38
		centerY := (result.Y-1)*img.Rows()/19 + img.Rows()/38
		center := image.Point{X: centerX, Y: centerY}

		green := color.RGBA{R: 0, G: 255, B: 0, A: 0}
		gocv.Circle(&markedImg, center, 20, green, 3)

		markedPath := filepath.Join(testDir, "marked.jpg")
		gocv.IMWrite(markedPath, markedImg)
	}

	return nil
}
