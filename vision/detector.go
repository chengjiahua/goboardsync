package vision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
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
	debugInfo := make(map[string]any)
	debugInfo["image_size"] = fmt.Sprintf("%dx%d", img.Cols(), img.Rows())
	debugInfo["move_number"] = moveNumber

	var corners []image.Point
	var color string
	var gridX, gridY int
	var err error

	debugInfo["step"] = "board_localization"
	debugInfo["board_localization_method"] = "fixed"

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

	warped, err := WarpBoard(img, corners)
	if err != nil {
		debugInfo["warp_error"] = err.Error()
		debugInfo["final_status"] = "failed_at_warp"
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

	isBlack := moveNumber%2 == 1
	if isBlack {
		gridX, gridY, err = boardblack(warped)
		if err != nil {
			debugInfo["detection_error"] = err.Error()
			debugInfo["final_status"] = "failed_at_detection"
			return Result{
				Move:       moveNumber,
				Color:      "B",
				X:          0,
				Y:          0,
				Confidence: 0,
				Debug:      debugInfo,
			}, nil
		}
		color = "B"
	} else {
		gridX, gridY, err = boardwhite(warped)
		if err != nil {
			debugInfo["detection_error"] = err.Error()
			debugInfo["final_status"] = "failed_at_detection"
			return Result{
				Move:       moveNumber,
				Color:      "W",
				X:          0,
				Y:          0,
				Confidence: 0,
				Debug:      debugInfo,
			}, nil
		}
		color = "W"
	}

	debugInfo["final_status"] = "success"
	result := Result{
		Move:       moveNumber,
		Color:      color,
		X:          gridX + 1,
		Y:          gridY + 1,
		Confidence: 0.8,
		Debug:      debugInfo,
	}

	return result, nil
}

// boardblack 识别黑棋
func boardblack(img gocv.Mat) (int, int, error) {
	// 使用统一的角标检测函数
	markerRect, found := findLastMoveMarker(img)
	if !found {
		return 0, 0, fmt.Errorf("未找到红色最后一手标记")
	}

	// 计算格子大小
	width := float64(img.Cols())
	height := float64(img.Rows())
	cellW := width / 19.0
	cellH := height / 19.0

	// 半径偏移：角标左上角 + 半径 = 棋子中心点
	radiusW := cellW / 2.0
	radiusH := cellH / 2.0
	centerX := float64(markerRect.Min.X) + radiusW
	centerY := float64(markerRect.Min.Y) + radiusH

	// 直接使用Floor取整索引
	gridX := int(math.Floor(centerX / cellW))
	gridY := int(math.Floor(centerY / cellH))

	// 边界检查
	if gridX >= 0 && gridX < 19 && gridY >= 0 && gridY < 19 {
		return gridX, gridY, nil
	} else {
		return 0, 0, fmt.Errorf("计算出的坐标超出范围: X:%d, Y:%d", gridX, gridY)
	}
}

// boardwhite 识别白棋
func boardwhite(img gocv.Mat) (int, int, error) {
	// 使用统一的角标检测函数
	markerRect, found := findLastMoveMarker(img)
	if !found {
		return 0, 0, fmt.Errorf("未检测到蓝色角标")
	}

	// 计算格子大小
	width := float64(img.Cols())
	height := float64(img.Rows())
	cellW := width / 19.0
	cellH := height / 19.0

	// 半径偏移：角标左上角 + 半径 = 棋子中心点
	radiusW := cellW / 2.0
	radiusH := cellH / 2.0
	centerX := float64(markerRect.Min.X) + radiusW
	centerY := float64(markerRect.Min.Y) + radiusH

	// 直接使用Floor取整索引
	gridX := int(math.Floor(centerX / cellW))
	gridY := int(math.Floor(centerY / cellH))

	// 边界检查
	if gridX >= 0 && gridX < 19 && gridY >= 0 && gridY < 19 {
		return gridX, gridY, nil
	} else {
		return 0, 0, fmt.Errorf("计算出的坐标超出范围: X:%d, Y:%d", gridX, gridY)
	}
}

// findBoardRect 寻找图片中的棋盘矩形区域
func findBoardRect(img gocv.Mat) image.Rectangle {
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(img, &gray, gocv.ColorBGRToGray)
	gocv.GaussianBlur(gray, &gray, image.Pt(5, 5), 0, 0, gocv.BorderDefault)

	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(gray, &edges, 50, 150)

	contours := gocv.FindContours(edges, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	maxArea := 0.0
	bestRect := image.Rect(0, 0, img.Cols(), img.Rows())

	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		rect := gocv.BoundingRect(contours.At(i))
		ratio := float64(rect.Dx()) / float64(rect.Dy())
		if area > maxArea && area > float64(img.Cols()*img.Cols()/4) && ratio > 0.9 && ratio < 1.1 {
			maxArea = area
			bestRect = rect
		}
	}

	if maxArea == 0 {
		w := img.Cols()
		return image.Rect(0, (img.Rows()-w)/2, w, (img.Rows()+w)/2)
	}
	return bestRect
}

// findLastMoveMarker 同时检测红色和蓝色，返回最大的色块区域
func findLastMoveMarker(img gocv.Mat) (image.Rectangle, bool) {
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	defer mask.Close()

	// 红色范围（黑棋最后一手）- 包含两个区间
	mRed1 := gocv.NewMat()
	mRed2 := gocv.NewMat()
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(0, 160, 100, 0), gocv.NewScalar(10, 255, 255, 0), &mRed1)
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(170, 160, 100, 0), gocv.NewScalar(180, 255, 255, 0), &mRed2)

	// 蓝色范围（白棋最后一手）
	mBlue := gocv.NewMat()
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(100, 160, 100, 0), gocv.NewScalar(140, 255, 255, 0), &mBlue)

	// 合并 Mask
	gocv.BitwiseOr(mRed1, mRed2, &mask)
	gocv.BitwiseOr(mask, mBlue, &mask)

	mRed1.Close()
	mRed2.Close()
	mBlue.Close()

	// 查找轮廓
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return image.Rectangle{}, false
	}

	// 获取面积最大的轮廓，避免干扰
	var bestRect image.Rectangle
	maxArea := 0.0
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		if area > maxArea {
			maxArea = area
			bestRect = gocv.BoundingRect(contours.At(i))
		}
	}

	return bestRect, maxArea > 0
}

// findMarker 寻找红色或蓝色角标
func findMarker(img gocv.Mat) (float64, float64, bool) {
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	defer mask.Close()

	ranges := [][]gocv.Scalar{
		{gocv.NewScalar(0, 150, 150, 0), gocv.NewScalar(10, 255, 255, 0)},
		{gocv.NewScalar(160, 150, 150, 0), gocv.NewScalar(180, 255, 255, 0)},
		{gocv.NewScalar(100, 150, 150, 0), gocv.NewScalar(130, 255, 255, 0)},
	}

	finalMask := gocv.NewMatWithSize(hsv.Rows(), hsv.Cols(), gocv.MatTypeCV8U)
	defer finalMask.Close()

	for _, r := range ranges {
		m := gocv.NewMat()
		l := gocv.NewMatWithSizeFromScalar(r[0], hsv.Rows(), hsv.Cols(), hsv.Type())
		u := gocv.NewMatWithSizeFromScalar(r[1], hsv.Rows(), hsv.Cols(), hsv.Type())
		gocv.InRange(hsv, l, u, &m)
		gocv.BitwiseOr(finalMask, m, &finalMask)
		m.Close()
		l.Close()
		u.Close()
	}

	contours := gocv.FindContours(finalMask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return 0, 0, false
	}

	var bestRect image.Rectangle
	maxA := 0.0
	for i := 0; i < contours.Size(); i++ {
		a := gocv.ContourArea(contours.At(i))
		if a > maxA {
			maxA = a
			bestRect = gocv.BoundingRect(contours.At(i))
		}
	}

	return float64(bestRect.Min.X+bestRect.Max.X) / 2.0,
		float64(bestRect.Min.Y+bestRect.Max.Y) / 2.0, true
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
