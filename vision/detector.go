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
	"strings"
	"time"

	"gocv.io/x/gocv"
)

const (
	BoardWarpSize = 1024
)

var FixedBoardCorners = map[string][]image.Point{
	"1200x2670": {
		{40, 536},
		{1160, 536},
		{1160, 1650},
		{40, 1650},
	},
}

type Result struct {
	Move       int             `json:"move"`
	Color      string          `json:"color"`
	X          int             `json:"x"`
	Y          int             `json:"y"`
	Confidence float64         `json:"confidence"`
	MarkerRect image.Rectangle `json:"marker_rect"`
	Debug      map[string]any  `json:"debug"`
}

type Detector struct {
	OCREndpoint string
}

func NewDetector() *Detector {
	return &Detector{
		OCREndpoint: "http://127.0.0.1:5001/ocr",
	}
}

func (d *Detector) FetchMoveNumberFromOCR(img gocv.Mat) (int, error) {
	if img.Empty() {
		return 0, fmt.Errorf("图片为空")
	}

	buf := new(bytes.Buffer)
	imgBytes, err := gocv.IMEncode(".jpg", img)
	if err != nil {
		return 0, fmt.Errorf("编码图片失败: %v", err)
	}
	defer imgBytes.Close()
	buf.Write(imgBytes.GetBytes())

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "image.jpg")
	if err != nil {
		return 0, fmt.Errorf("创建表单文件失败: %v", err)
	}

	_, err = io.Copy(part, buf)
	if err != nil {
		return 0, fmt.Errorf("写入图片数据失败: %v", err)
	}
	writer.Close()

	client := &http.Client{Timeout: 10 * time.Second}
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
		respData, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("OCR 响应错误: %d, 响应: %s", resp.StatusCode, string(respData))
	}

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %v", err)
	}

	var allText strings.Builder

	var results []struct {
		Words string `json:"words"`
	}
	err = json.Unmarshal(respData, &results)
	if err == nil && len(results) > 0 {
		for _, r := range results {
			allText.WriteString(r.Words)
			allText.WriteString(" ")
		}
	} else {
		var wrapper struct {
			Results []struct {
				Words string `json:"words"`
			} `json:"results"`
		}
		if err2 := json.Unmarshal(respData, &wrapper); err2 == nil && len(wrapper.Results) > 0 {
			for _, r := range wrapper.Results {
				allText.WriteString(r.Words)
				allText.WriteString(" ")
			}
		} else {
			allText.WriteString(string(respData))
		}
	}

	fullText := strings.TrimSpace(allText.String())
	moveNumber := extractMoveNumber(fullText)

	if moveNumber > 0 {
		return moveNumber, nil
	}

	return 0, fmt.Errorf("未识别到有效手数")
}

func extractMoveNumber(text string) int {
	if text == "" {
		return 0
	}

	patterns := []struct {
		name     string
		pattern  string
		priority int
	}{
		{"中文格式", `第\s*(\d+)\s*手`, 1},
		{"纯数字+手", `(\d+)\s*手`, 2},
		{"井号格式", `#\s*(\d+)`, 3},
		{"move格式", `(?i)move\s*:?\s*(\d+)`, 4},
		{"Step格式", `(?i)step\s*:?\s*(\d+)`, 5},
		{"最后数字", `(\d+)$`, 6},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			num, err := strconv.Atoi(matches[1])
			if err == nil && num > 0 && num < 2000 {
				return num
			}
		}
	}

	nums := regexp.MustCompile(`(\d+)`).FindAllString(text, -1)

	for i := len(nums) - 1; i >= 0; i-- {
		if num, err := strconv.Atoi(nums[i]); err == nil && num > 0 && num < 500 {
			return num
		}
	}

	return 0
}

func WarpBoard(img gocv.Mat, corners []image.Point) (gocv.Mat, error) {
	if len(corners) != 4 {
		return gocv.Mat{}, fmt.Errorf("需要4个角点")
	}

	dst := []image.Point{
		{0, 0},
		{BoardWarpSize, 0},
		{BoardWarpSize, BoardWarpSize},
		{0, BoardWarpSize},
	}

	srcPoints := gocv.NewPointVectorFromPoints(corners)
	defer srcPoints.Close()

	dstPoints := gocv.NewPointVectorFromPoints(dst)
	defer dstPoints.Close()

	M := gocv.GetPerspectiveTransform(srcPoints, dstPoints)
	if M.Empty() {
		return gocv.Mat{}, fmt.Errorf("计算透视变换矩阵失败")
	}

	warped := gocv.NewMat()
	gocv.WarpPerspective(img, &warped, M, image.Point{X: BoardWarpSize, Y: BoardWarpSize})

	return warped, nil
}

func DetectLastMoveCoord(img gocv.Mat, moveNumber int) (Result, error) {
	debugInfo := make(map[string]any)
	debugInfo["image_size"] = fmt.Sprintf("%dx%d", img.Cols(), img.Rows())
	debugInfo["move_number"] = moveNumber

	var corners []image.Point
	var color string
	var gridX, gridY int
	var markerRect image.Rectangle
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
		}, fmt.Errorf("不支持的图片分辨率: %dx%d", img.Cols(), img.Rows())
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

	fmt.Printf("[检测] 开始检测最后一手，moveNumber=%d\n", moveNumber)

	isBlack := moveNumber%2 == 1
	if isBlack {
		markerRect, gridX, gridY, err = boardblack(warped)
		if err != nil {
			debugInfo["detection_error"] = err.Error()
			debugInfo["final_status"] = "failed_at_detection"
			return Result{
				Move:       moveNumber,
				Color:      "B",
				X:          0,
				Y:          0,
				Confidence: 0,
				MarkerRect: markerRect,
				Debug:      debugInfo,
			}, nil
		}
		color = "B"
		fmt.Printf("[检测] 黑棋，检测到标记位置: %v\n", markerRect)
	} else {
		markerRect, gridX, gridY, err = boardwhite(warped)
		if err != nil {
			debugInfo["detection_error"] = err.Error()
			debugInfo["final_status"] = "failed_at_detection"
			return Result{
				Move:       moveNumber,
				Color:      "W",
				X:          0,
				Y:          0,
				Confidence: 0,
				MarkerRect: markerRect,
				Debug:      debugInfo,
			}, nil
		}
		color = "W"
		fmt.Printf("[检测] 白棋，检测到标记位置: %v\n", markerRect)
	}

	debugInfo["final_status"] = "success"
	result := Result{
		Move:       moveNumber,
		Color:      color,
		X:          gridX + 1,
		Y:          gridY + 1,
		Confidence: 0.8,
		MarkerRect: markerRect,
		Debug:      debugInfo,
	}

	fmt.Printf("[检测] 完成，坐标: %d-%s%d\n", result.Move, string(rune('A'+result.X-1)), result.Y)

	return result, nil
}

func calculateGrid(markerRect image.Rectangle, width, height int) (int, int, image.Point) {
	cellW := float64(width) / 19.0
	cellH := float64(height) / 19.0

	centerX := float64(markerRect.Min.X) + cellW/2.0
	centerY := float64(markerRect.Min.Y) + cellH/2.0

	gridX := int(math.Floor(centerX / cellW))
	gridY := int(math.Floor(centerY / cellH))

	return clamp(gridX, 0, 18), clamp(gridY, 0, 18), image.Pt(int(centerX), int(centerY))
}

func boardblack(img gocv.Mat) (image.Rectangle, int, int, error) {
	markerRect, found := findLastMoveMarker(img)
	if !found {
		return image.Rectangle{}, 0, 0, fmt.Errorf("未找到红色最后一手标记")
	}

	gridX, gridY, _ := calculateGrid(markerRect, img.Cols(), img.Rows())

	return markerRect, gridX, gridY, nil
}

func boardwhite(img gocv.Mat) (image.Rectangle, int, int, error) {
	markerRect, found := findLastMoveMarker(img)
	if !found {
		return image.Rectangle{}, 0, 0, fmt.Errorf("未检测到蓝色角标")
	}

	gridX, gridY, _ := calculateGrid(markerRect, img.Cols(), img.Rows())

	return markerRect, gridX, gridY, nil
}


func findLastMoveMarker(img gocv.Mat) (image.Rectangle, bool) {
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	defer mask.Close()

	mRed1 := gocv.NewMat()
	mRed2 := gocv.NewMat()
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(0, 160, 100, 0), gocv.NewScalar(10, 255, 255, 0), &mRed1)
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(170, 160, 100, 0), gocv.NewScalar(180, 255, 255, 0), &mRed2)

	mBlue := gocv.NewMat()
	gocv.InRangeWithScalar(hsv, gocv.NewScalar(100, 160, 100, 0), gocv.NewScalar(140, 255, 255, 0), &mBlue)

	gocv.BitwiseOr(mRed1, mRed2, &mask)
	gocv.BitwiseOr(mask, mBlue, &mask)

	mRed1.Close()
	mRed2.Close()
	mBlue.Close()

	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return image.Rectangle{}, false
	}

	var bestRect image.Rectangle
	maxArea := 0.0
	for i := 0; i < contours.Size(); i++ {
		area := gocv.ContourArea(contours.At(i))
		if area > maxArea {
			maxArea = area
			bestRect = gocv.BoundingRect(contours.At(i))
		}
	}

	fmt.Printf("[HSV检测] 找到 %d 个轮廓，最大面积: %.2f\n", contours.Size(), maxArea)

	return bestRect, maxArea > 0
}

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

func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
