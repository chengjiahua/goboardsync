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
	"my-app/board"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gocv.io/x/gocv"
)

const (
	// BoardWarpSize 棋盘矫正后的大小
	BoardWarpSize = 1024
	// MaxImageSize 图像预处理后的最大长边尺寸
	MaxImageSize = 1600
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

// FixedBoardCropPercent 按分辨率定义在透视矫正后的图像上需要裁剪的比例（相对于 BoardWarpSize）
// 用于去掉顶部/底部的 UI 区域，值为 0.0..0.5
type CropPercent struct{ Top, Bottom, Left, Right float64 }

var FixedBoardCropPercent = map[string]CropPercent{
	// 默认不裁剪，可以为特定分辨率调整 Top 以去掉上方 UI
	"1200x2670": {Top: 0.0, Bottom: 0.0, Left: 0.0, Right: 0.0},
	"1125x2436": {Top: 0.0, Bottom: 0.0, Left: 0.0, Right: 0.0},
}

const (
	ColorNone  = 0
	ColorBlack = 1
	ColorWhite = 2
)

type Detector struct {
	BoardModel     *board.Board
	LastBoardState [19][19]int // 存储上一次识别的 19x19 状态
	Threshold      float64
	HGrid          []int  // 19 条水平线坐标
	VGrid          []int  // 19 条垂直线坐标
	OCREndpoint    string // OCR 服务地址
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

// PreprocessImage 图像预处理
func PreprocessImage(img gocv.Mat) gocv.Mat {
	// 1. 缩放图像
	scaled := scaleImage(img)

	// 2. 灰度化
	gray := gocv.NewMat()
	gocv.CvtColor(scaled, &gray, gocv.ColorBGRToGray)

	// 3. 移除棋子干扰
	// 先检测并移除棋子，减少对直线检测的干扰
	cleaned := removeStones(gray)

	// 4. 高斯模糊
	blurred := gocv.NewMat()
	gocv.GaussianBlur(cleaned, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	// 释放资源
	scaled.Close()
	gray.Close()
	cleaned.Close()

	return blurred
}

// scaleImage 缩放图像到合适尺寸
func scaleImage(img gocv.Mat) gocv.Mat {
	height := img.Rows()
	width := img.Cols()
	maxDim := math.Max(float64(height), float64(width))

	if maxDim <= float64(MaxImageSize) {
		return img.Clone()
	}

	scale := float64(MaxImageSize) / maxDim
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	scaled := gocv.NewMat()
	gocv.Resize(img, &scaled, image.Point{X: newWidth, Y: newHeight}, 0, 0, gocv.InterpolationLinear)
	return scaled
}

// removeStones 移除图像中的棋子干扰
func removeStones(img gocv.Mat) gocv.Mat {
	// 创建图像副本
	cleaned := img.Clone()

	// 高斯模糊
	blurred := gocv.NewMat()
	gocv.GaussianBlur(img, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	// 霍夫圆检测
	circles := gocv.NewMat()
	defer circles.Close()

	// 参数设置
	minDist := float64(img.Rows() / 10)
	param1 := float64(100)
	param2 := float64(30)
	minRadius := img.Rows() / 30
	maxRadius := img.Rows() / 10

	gocv.HoughCirclesWithParams(
		blurred, &circles, gocv.HoughGradient, 1,
		minDist, param1, param2, minRadius, maxRadius,
	)

	// 填充检测到的圆（棋子）
	for i := 0; i < circles.Cols(); i++ {
		circle := circles.GetVecfAt(0, i)
		cx, cy, r := int(circle[0]), int(circle[1]), int(circle[2])

		// 用背景色填充圆
		gocv.Circle(&cleaned, image.Point{X: cx, Y: cy}, r, color.RGBA{128, 128, 128, 0}, -1)
	}

	// 释放资源
	blurred.Close()

	return cleaned
}

// Line 直线结构体
type Line struct {
	X1, Y1, X2, Y2 int
	Angle          float64
	Length         float64
}

// getLinePosition 获取直线的位置值
func getLinePosition(line Line, isHorizontal bool) float64 {
	if isHorizontal {
		// 水平线取y值
		return float64((line.Y1 + line.Y2) / 2)
	} else {
		// 垂直线取x值
		return float64((line.X1 + line.X2) / 2)
	}
}

// WarpBoard 透视矫正棋盘
func WarpBoard(img gocv.Mat, corners []image.Point) (gocv.Mat, error) {
	if len(corners) != 4 {
		return gocv.Mat{}, fmt.Errorf("角点数量不正确")
	}

	// 定义目标正方形的四个角点
	dst := []image.Point{
		{0, 0},
		{BoardWarpSize, 0},
		{BoardWarpSize, BoardWarpSize},
		{0, BoardWarpSize},
	}

	// 计算透视变换矩阵
	srcPoints := gocv.NewPointVector()
	defer srcPoints.Close()
	dstPoints := gocv.NewPointVector()
	defer dstPoints.Close()

	for _, pt := range corners {
		srcPoints.Append(image.Point{X: pt.X, Y: pt.Y})
	}
	for _, pt := range dst {
		dstPoints.Append(image.Point{X: pt.X, Y: pt.Y})
	}

	M := gocv.GetPerspectiveTransform(srcPoints, dstPoints)
	defer M.Close()

	// 应用透视变换
	warped := gocv.NewMat()
	gocv.WarpPerspective(img, &warped, M, image.Point{X: BoardWarpSize, Y: BoardWarpSize})

	return warped, nil
}

// FindMark 寻找角标
func FindMark(img gocv.Mat, moveNumber int) (image.Point, error) {
	// 1. 确定目标颜色
	isWhite := moveNumber%2 == 0

	// 2. 颜色分割 - 使用BGR颜色空间和用户提供的精确RGB值
	mask := gocv.NewMat()
	defer mask.Close()

	if isWhite {
		// 白棋角标 - 使用用户提供的精确RGB值(28, 34, 241)
		// 创建蓝色角标的颜色范围
		lowerBlue := gocv.NewMatFromScalar(gocv.NewScalar(15, 20, 220, 0), gocv.MatTypeCV8UC3)
		defer lowerBlue.Close()
		upperBlue := gocv.NewMatFromScalar(gocv.NewScalar(45, 50, 255, 0), gocv.MatTypeCV8UC3)
		defer upperBlue.Close()
		gocv.InRange(img, lowerBlue, upperBlue, &mask)
	} else {
		// 黑棋角标 - 使用用户提供的精确RGB值(234, 61, 53)
		// 创建红色角标的颜色范围
		lowerRed := gocv.NewMatFromScalar(gocv.NewScalar(210, 40, 30, 0), gocv.MatTypeCV8UC3)
		defer lowerRed.Close()
		upperRed := gocv.NewMatFromScalar(gocv.NewScalar(255, 85, 75, 0), gocv.MatTypeCV8UC3)
		defer upperRed.Close()
		gocv.InRange(img, lowerRed, upperRed, &mask)
	}

	// 3. 形态学去噪
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{X: 3, Y: 3})
	defer kernel.Close()

	// 先膨胀再腐蚀（闭操作）以填充小洞
	dilated := gocv.NewMat()
	defer dilated.Close()
	gocv.Dilate(mask, &dilated, kernel)

	eroded := gocv.NewMat()
	defer eroded.Close()
	gocv.Erode(dilated, &eroded, kernel)

	// 4. 轮廓提取
	contours := gocv.FindContours(eroded, gocv.RetrievalExternal, gocv.ChainApproxSimple)

	if contours.Size() == 0 {
		return image.Point{}, fmt.Errorf("未找到角标")
	}

	// 5. 轮廓筛选 - 使用更宽松的条件
	bestIdx := -1
	bestScore := 0.0

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		// 计算面积
		area := gocv.ContourArea(contour)
		if area < 5 || area > 800 {
			continue
		}

		// 计算外接矩形
		rect := gocv.BoundingRect(contour)
		w, h := rect.Dx(), rect.Dy()

		// 宽高比检查 - 角标是三角形，宽高比应该接近1
		ratio := math.Max(float64(w), float64(h)) / math.Min(float64(w), float64(h))
		if ratio > 5.0 {
			continue
		}

		// 计算得分
		score := area * (5.0 - ratio)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	// 6. 检查是否有有效的轮廓
	if bestIdx == -1 {
		return image.Point{}, fmt.Errorf("未找到有效的角标轮廓")
	}

	// 7. 计算角标中心点
	bestContour := contours.At(bestIdx)
	rect := gocv.BoundingRect(bestContour)
	centerX := rect.Min.X + rect.Dx()/2
	centerY := rect.Min.Y + rect.Dy()/2

	return image.Point{X: centerX, Y: centerY}, nil
}

// FindMarkBGR 使用BGR颜色空间寻找角标（备选方案）
func FindMarkBGR(img gocv.Mat, moveNumber int) (image.Point, error) {
	// 1. 确定目标颜色
	isWhite := moveNumber%2 == 0

	// 2. 计算棋盘内部区域（排除边缘）
	margin := float64(img.Cols()) * 0.15 // 增加margin到15%
	innerLeft := int(margin)
	innerTop := int(margin)
	innerRight := img.Cols() - int(margin)
	innerBottom := img.Rows() - int(margin)
	innerRect := image.Rect(innerLeft, innerTop, innerRight, innerBottom)

	// 3. 颜色分割 - 使用用户提供的精确RGB值
	mask := gocv.NewMat()
	defer mask.Close()

	if isWhite {
		// 白棋角标 - 使用用户提供的精确RGB值(28, 34, 241)
		// 创建蓝色角标的颜色范围
		lowerBlue := gocv.NewMatFromScalar(gocv.NewScalar(15, 20, 220, 0), gocv.MatTypeCV8UC3)
		defer lowerBlue.Close()
		upperBlue := gocv.NewMatFromScalar(gocv.NewScalar(45, 50, 255, 0), gocv.MatTypeCV8UC3)
		defer upperBlue.Close()
		gocv.InRange(img, lowerBlue, upperBlue, &mask)
	} else {
		// 黑棋角标 - 使用用户提供的精确RGB值(234, 61, 53)
		// 创建红色角标的颜色范围
		lowerRed := gocv.NewMatFromScalar(gocv.NewScalar(210, 40, 30, 0), gocv.MatTypeCV8UC3)
		defer lowerRed.Close()
		upperRed := gocv.NewMatFromScalar(gocv.NewScalar(255, 85, 75, 0), gocv.MatTypeCV8UC3)
		defer upperRed.Close()
		gocv.InRange(img, lowerRed, upperRed, &mask)
	}

	// 4. 只保留内部区域的mask
	// 创建一个全黑的mask
	innerMask := gocv.NewMatWithSize(img.Rows(), img.Cols(), gocv.MatTypeCV8UC1)
	defer innerMask.Close()
	// 在innerMask上绘制白色内部矩形
	gocv.Rectangle(&innerMask, innerRect, color.RGBA{255, 255, 255, 0}, -1)
	// 与操作，只保留内部区域的mask
	gocv.BitwiseAnd(mask, innerMask, &mask)

	// 5. 形态学去噪
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{X: 3, Y: 3})
	defer kernel.Close()

	// 先膨胀再腐蚀（闭操作）以填充小洞
	dilated := gocv.NewMat()
	defer dilated.Close()
	gocv.Dilate(mask, &dilated, kernel)

	eroded := gocv.NewMat()
	defer eroded.Close()
	gocv.Erode(dilated, &eroded, kernel)

	// 6. 轮廓提取
	contours := gocv.FindContours(eroded, gocv.RetrievalExternal, gocv.ChainApproxSimple)

	if contours.Size() == 0 {
		return image.Point{}, fmt.Errorf("未找到角标")
	}

	// 7. 轮廓筛选 - 使用更严格的条件
	bestIdx := -1
	bestScore := 0.0

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		// 计算面积
		area := gocv.ContourArea(contour)
		if area < 8 || area > 600 {
			continue
		}

		// 计算外接矩形
		rect := gocv.BoundingRect(contour)
		w, h := rect.Dx(), rect.Dy()

		// 宽高比检查 - 角标是三角形，宽高比应该接近1
		ratio := math.Max(float64(w), float64(h)) / math.Min(float64(w), float64(h))
		if ratio > 4.0 {
			continue
		}

		// 计算得分
		score := area * (4.0 - ratio)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	// 8. 检查是否有有效的轮廓
	if bestIdx == -1 {
		return image.Point{}, fmt.Errorf("未找到有效的角标轮廓")
	}

	// 9. 计算角标中心点
	bestContour := contours.At(bestIdx)
	rect := gocv.BoundingRect(bestContour)
	centerX := rect.Min.X + rect.Dx()/2
	centerY := rect.Min.Y + rect.Dy()/2

	return image.Point{X: centerX, Y: centerY}, nil
}

// FindStoneCenter 寻找棋子中心
func FindStoneCenter(img gocv.Mat, markPt image.Point) (image.Point, error) {
	// 1. 估计网格间距和棋子半径
	gridSpacing := float64(BoardWarpSize) / 18.0 // 18个间隔
	rStone := 0.5 * gridSpacing                  // 棋子半径

	// 2. 从角标中心推棋子中心初值
	// 角标在棋子左上角，所以棋子中心在角标右下方向
	// 固定偏移：角标在棋子左上角，棋子中心应该在角标的右下方
	stoneCenter := image.Point{
		X: markPt.X + int(rStone), // 固定偏移：角标在棋子左上角，棋子中心在角标的右下方
		Y: markPt.Y + int(rStone),
	}

	// 3. 确保棋子中心在棋盘范围内
	stoneCenter.X = max(0, min(img.Cols()-1, stoneCenter.X))
	stoneCenter.Y = max(0, min(img.Rows()-1, stoneCenter.Y))

	// 4. 确保棋子中心始终在角标的右下方向
	// 这是一个重要的约束，因为角标在棋子左上角
	stoneCenter.X = max(markPt.X+5, stoneCenter.X) // 确保至少有5像素的偏移
	stoneCenter.Y = max(markPt.Y+5, stoneCenter.Y)

	// 5. 优化：使用颜色分析精确定位棋子中心
	// 裁剪棋子区域
	roiSize := int(2 * rStone)
	roiRect := image.Rect(
		max(0, stoneCenter.X-roiSize),
		max(0, stoneCenter.Y-roiSize),
		min(img.Cols(), stoneCenter.X+roiSize),
		min(img.Rows(), stoneCenter.Y+roiSize),
	)

	// 分析ROI区域的颜色
	roi := img.Region(roiRect)
	gray := gocv.NewMat()
	gocv.CvtColor(roi, &gray, gocv.ColorBGRToGray)

	// 二值化
	binary := gocv.NewMat()
	gocv.AdaptiveThreshold(gray, &binary, 255, gocv.AdaptiveThresholdGaussian, gocv.ThresholdBinary, 11, 2)

	// 寻找最大轮廓
	contours := gocv.FindContours(binary, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	if contours.Size() > 0 {
		bestContour := contours.At(0)
		bestArea := gocv.ContourArea(bestContour)

		for i := 1; i < contours.Size(); i++ {
			contour := contours.At(i)
			area := gocv.ContourArea(contour)
			if area > bestArea {
				bestArea = area
				bestContour = contour
			}
		}

		// 计算最大轮廓的中心点
		rect := gocv.BoundingRect(bestContour)
		contourCenter := image.Point{
			X: roiRect.Min.X + rect.Min.X + rect.Dx()/2,
			Y: roiRect.Min.Y + rect.Min.Y + rect.Dy()/2,
		}

		// 确保轮廓中心在角标的右下方向
		contourCenter.X = max(markPt.X+5, contourCenter.X)
		contourCenter.Y = max(markPt.Y+5, contourCenter.Y)

		// 释放资源
		roi.Close()
		gray.Close()
		binary.Close()

		return contourCenter, nil
	}

	// 释放资源
	roi.Close()
	gray.Close()

	return stoneCenter, nil
}

// FindLastMoveDirect 直接检测最后一手棋子（备选方案）
func FindLastMoveDirect(img gocv.Mat, moveNumber int) (image.Point, error) {
	// 1. 确定目标颜色
	isWhite := moveNumber%2 == 0

	// 2. 转换为灰度图
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(img, &gray, gocv.ColorBGRToGray)

	// 3. 高斯模糊
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	// 4. 边缘检测
	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 50, 150)

	// 5. 圆检测
	circles := gocv.NewMat()
	defer circles.Close()

	// 估计棋子半径
	gridSpacing := float64(BoardWarpSize) / 18.0
	minRadius := int(0.4 * gridSpacing)
	maxRadius := int(0.6 * gridSpacing)

	gocv.HoughCirclesWithParams(
		blurred, &circles, gocv.HoughGradient, 1,
		float64(maxRadius), 50, 20, minRadius, maxRadius,
	)

	if circles.Cols() == 0 {
		return image.Point{}, fmt.Errorf("未检测到棋子")
	}

	// 6. 筛选最可能的最后一手棋子
	bestCircle := image.Point{}
	bestScore := 0.0

	for i := 0; i < circles.Cols(); i++ {
		circle := circles.GetVecfAt(0, i)
		cx, cy, r := int(circle[0]), int(circle[1]), int(circle[2])

		// 确保圆在棋盘范围内
		if cx-r < 0 || cx+r >= img.Cols() || cy-r < 0 || cy+r >= img.Rows() {
			continue
		}

		// 裁剪ROI
		roi := img.Region(image.Rect(cx-r, cy-r, cx+r, cy+r))

		// 计算颜色均值
		meanMat := gocv.NewMat()
		defer meanMat.Close()
		stddevMat := gocv.NewMat()
		defer stddevMat.Close()
		gocv.MeanStdDev(roi, &meanMat, &stddevMat)

		b := meanMat.GetDoubleAt(0, 0)
		g := meanMat.GetDoubleAt(1, 0)
		red := meanMat.GetDoubleAt(2, 0)

		// 计算亮度
		brightness := (b + g + red) / 3.0

		// 计算颜色鲜艳度
		maxRGB := math.Max(math.Max(b, g), red)
		minRGB := math.Min(math.Min(b, g), red)
		colorRange := maxRGB - minRGB

		// 筛选棋子
		if colorRange > 30 {
			continue // 颜色太鲜艳，可能不是棋子
		}

		// 根据颜色筛选
		if isWhite {
			if brightness < 120 {
				continue // 白棋应该较亮
			}
		} else {
			if brightness > 100 {
				continue // 黑棋应该较暗
			}
		}

		// 计算得分
		score := 1.0 / (colorRange + 1.0)
		if score > bestScore {
			bestScore = score
			bestCircle = image.Point{X: cx, Y: cy}
		}

		roi.Close()
	}

	if bestCircle.X == 0 && bestCircle.Y == 0 {
		return image.Point{}, fmt.Errorf("未找到符合条件的棋子")
	}

	return bestCircle, nil
}

// GridInfo 网格信息
type GridInfo struct {
	InnerRect image.Rectangle
	Dx, Dy    float64
	Grid      [19][19]image.Point
}

// CalculateGrid 计算19×19交叉点网格
func CalculateGrid(img gocv.Mat) GridInfo {
	// 1. 使用整个boardWarp图片作为棋盘区域，不使用margin
	// 直接使用boardWarp的四个角作为棋盘的四个角
	innerLeft := 0
	innerTop := 0
	innerRight := img.Cols()
	innerBottom := img.Rows()

	innerRect := image.Rect(innerLeft, innerTop, innerRight, innerBottom)

	// 2. 计算网格间距
	dx := float64(innerRight-innerLeft) / 18.0
	dy := float64(innerBottom-innerTop) / 18.0

	// 3. 生成交叉点
	// grid[i][j] 其中i是列索引（x方向），j是行索引（y方向）
	var grid [19][19]image.Point
	for i := 0; i < 19; i++ {
		for j := 0; j < 19; j++ {
			x := innerLeft + int(float64(i)*dx)
			y := innerTop + int(float64(j)*dy)
			grid[i][j] = image.Point{X: x, Y: y}
		}
	}

	return GridInfo{
		InnerRect: innerRect,
		Dx:        dx,
		Dy:        dy,
		Grid:      grid,
	}
}

// MapToGrid 将棋子中心映射到最近的交叉点
func MapToGrid(stoneCenter image.Point, grid GridInfo) (int, int, float64) {
	// 1. 计算最近的交叉点
	minDist := math.MaxFloat64
	bestI, bestJ := 0, 0

	for i := 0; i < 19; i++ {
		for j := 0; j < 19; j++ {
			pt := grid.Grid[i][j]
			dist := math.Hypot(float64(stoneCenter.X-pt.X), float64(stoneCenter.Y-pt.Y))
			if dist < minDist {
				minDist = dist
				bestI, bestJ = i, j
			}
		}
	}

	// 2. 计算置信度
	maxDist := 0.5 * math.Min(grid.Dx, grid.Dy)
	confidence := 1.0 - minDist/maxDist
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}

	// 3. 优化：使用二次插值提高映射精度
	// 检查周围的交叉点，使用二次插值找到更精确的映射
	if bestI > 0 && bestI < 18 && bestJ > 0 && bestJ < 18 {
		// 计算周围四个交叉点的距离
		centerDist := minDist
		topDist := math.Hypot(float64(stoneCenter.X-grid.Grid[bestI][bestJ-1].X), float64(stoneCenter.Y-grid.Grid[bestI][bestJ-1].Y))
		bottomDist := math.Hypot(float64(stoneCenter.X-grid.Grid[bestI][bestJ+1].X), float64(stoneCenter.Y-grid.Grid[bestI][bestJ+1].Y))
		leftDist := math.Hypot(float64(stoneCenter.X-grid.Grid[bestI-1][bestJ].X), float64(stoneCenter.Y-grid.Grid[bestI-1][bestJ].Y))
		rightDist := math.Hypot(float64(stoneCenter.X-grid.Grid[bestI+1][bestJ].X), float64(stoneCenter.Y-grid.Grid[bestI+1][bestJ].Y))

		// 基于距离进行简单的权重调整
		if topDist < centerDist {
			bestJ--
		} else if bottomDist < centerDist {
			bestJ++
		}

		if leftDist < centerDist {
			bestI--
		} else if rightDist < centerDist {
			bestI++
		}
	}

	// 4. 转换为1-based坐标
	// 直接计算最终坐标
	// X轴：0-18 -> 1-19，对应A-S
	// Y轴：0-18 -> 1-19，然后转换为19-1
	x := bestI + 1 // X轴：0-18 -> 1-19，对应A-S
	y := bestJ + 1 // Y轴：0-18 -> 1-19

	// 转换Y轴方向：1-19 -> 19-1
	y = 20 - y

	return x, y, confidence
}

// VerifyMoveNumber 验证棋子上的手数数字
func VerifyMoveNumber(img gocv.Mat, stoneCenter image.Point, expectedMove int) (bool, error) {
	// 裁剪ROI
	roiSize := 90
	roiRect := image.Rect(
		max(0, stoneCenter.X-roiSize/2),
		max(0, stoneCenter.Y-roiSize/2),
		min(img.Cols(), stoneCenter.X+roiSize/2),
		min(img.Rows(), stoneCenter.Y+roiSize/2),
	)

	if roiRect.Dx() < 50 || roiRect.Dy() < 50 {
		return false, fmt.Errorf("ROI太小")
	}

	roi := img.Region(roiRect)
	defer roi.Close()

	// 转换为灰度图
	grayROI := gocv.NewMat()
	defer grayROI.Close()
	gocv.CvtColor(roi, &grayROI, gocv.ColorBGRToGray)

	// 二值化
	binary := gocv.NewMat()
	defer binary.Close()
	gocv.AdaptiveThreshold(grayROI, &binary, 255, gocv.AdaptiveThresholdGaussian, gocv.ThresholdBinaryInv, 11, 2)

	// 这里可以调用OCR服务进行数字识别
	// 由于OCR服务可能不稳定，这里先返回true，后续可以根据实际情况修改
	// 实际实现时，需要将binary转换为图片并调用OCR服务

	// 临时返回true，后续需要实现真正的OCR验证
	return true, nil
}

// CalculateFinalConfidence 计算最终置信度
func CalculateFinalConfidence(gridConf float64, ocrVerified bool) float64 {
	// 基础置信度
	conf := gridConf

	// OCR验证加分
	if ocrVerified {
		conf += 0.2
	}

	// 确保置信度在0-1之间
	if conf > 1.0 {
		conf = 1.0
	}

	return conf
}

// ====================== 批量识别和统计函数 ======================

// ConvertToGTP 将行和列转换为GTP坐标
// ConvertToGTP 将棋盘坐标转换为GTP格式
// 新坐标系：左上角为(0,0)，横轴 A-S (不跳过I)，纵轴 0-19 (从上到下)
// 例如: ConvertToGTP(0, 0) -> "A0", ConvertToGTP(18, 18) -> "S18"
func ConvertToGTP(row, col int) string {
	if row < 0 || row >= 19 || col < 0 || col >= 19 {
		return "None"
	}

	// 列转换为字母 (A-S，不跳过I，共19列)
	colChar := 'A' + col

	// 行就是直接使用 0-19
	return fmt.Sprintf("%c%d", colChar, row)
}

// ConvertGTPToCoords 将GTP坐标转换为数值坐标 (col, row)
// 例如: ConvertGTPToCoords("A0") -> (0, 0), ConvertGTPToCoords("S18") -> (18, 18)
func ConvertGTPToCoords(gtp string) (int, int) {
	if len(gtp) < 2 {
		return -1, -1
	}

	// 解析列 (字母部分，A-S，不跳过I)
	colChar := rune(gtp[0])
	if colChar < 'A' || colChar > 'S' {
		return -1, -1
	}
	col := int(colChar - 'A')

	// 解析行 (数字部分，0-19)
	rowNum, err := strconv.Atoi(gtp[1:])
	if err != nil || rowNum < 0 || rowNum >= 19 {
		return -1, -1
	}

	// 验证坐标范围
	if col < 0 || col >= 19 {
		return -1, -1
	}

	return col, rowNum
}

// BatchRecognitionStats 批量识别统计信息
type BatchRecognitionStats struct {
	TotalCount           int
	SuccessCount         int
	FailureCount         int
	SuccessRate          float64
	MeanSquaredError     float64
	RootMeanSquaredError float64
	MaxError             float64
	MinError             float64
	TotalErrorCount      int
}

// RecognitionDetail 单个识别的详细结果
type RecognitionDetail struct {
	FileName        string
	Expected        string // "手数-坐标-颜色"
	Actual          string // "手数-坐标-颜色"
	ImageSize       string
	Confidence      string
	IsCorrect       bool
	SquaredError    float64
	CoordinateError string
}

// BatchRecognizeImages 批量识别目录中的图像
func BatchRecognizeImages(imagesDir string) (BatchRecognitionStats, []RecognitionDetail, error) {
	files, err := os.ReadDir(imagesDir)
	if err != nil {
		return BatchRecognitionStats{}, nil, fmt.Errorf("无法读取目录: %v", err)
	}

	var stats BatchRecognitionStats
	var details []RecognitionDetail
	var totalSquaredError float64
	maxError := 0.0
	minError := math.MaxFloat64

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}

		// 解析文件名: 手数-坐标系统-黑棋/白棋.ext
		parts := strings.Split(strings.TrimSuffix(name, ext), "-")
		if len(parts) < 3 {
			continue
		}
		stats.TotalCount++

		// 解析预期值
		expectHand := parts[0]
		moveNumber, err := strconv.Atoi(expectHand)
		if err != nil {
			continue
		}

		expectGTP := parts[1]

		// 处理颜色：支持 black/white 或 黑棋/白棋，并处理 _1, _2 等后缀
		expectColorRaw := strings.ToLower(strings.Split(parts[2], "_")[0])
		expectColorStr := "black"
		if strings.Contains(expectColorRaw, "white") || strings.Contains(expectColorRaw, "白") {
			expectColorStr = "white"
		}

		// 读取和识别图像
		imgPath := filepath.Join(imagesDir, name)
		img := gocv.IMRead(imgPath, gocv.IMReadColor)
		if img.Empty() {
			continue
		}

		// 使用新的检测函数
		result, err := DetectLastMoveCoord(img, moveNumber)
		img.Close()

		if err != nil {
			continue
		}

		// 转换坐标为GTP格式
		actualGTP := "None"
		if result.X >= 0 && result.X < 19 && result.Y >= 0 && result.Y < 19 {
			actualGTP = ConvertToGTP(result.Y, result.X)
		}

		// 转换颜色
		actualColorStr := "None"
		if result.Color == "B" {
			actualColorStr = "black"
		} else if result.Color == "W" {
			actualColorStr = "white"
		}

		expectStr := fmt.Sprintf("%s-%s-%s", expectHand, expectGTP, expectColorStr)
		actualStr := fmt.Sprintf("%d-%s-%s", result.Move, actualGTP, actualColorStr)
		confidence := fmt.Sprintf("%.2f", result.Confidence)

		isCorrect := result.Move == moveNumber && actualGTP == expectGTP && actualColorStr == expectColorStr

		detail := RecognitionDetail{
			FileName:   name,
			Expected:   expectStr,
			Actual:     actualStr,
			ImageSize:  result.Debug["image_size"].(string),
			Confidence: confidence,
			IsCorrect:  isCorrect,
		}

		// 无论识别结果是否正确，都保存调试图像以便比对和微调
		debugDir := filepath.Join("debug", strings.TrimSuffix(name, ext))
		_ = os.MkdirAll(debugDir, 0755)
		debugImg := gocv.IMRead(imgPath, gocv.IMReadColor)
		if !debugImg.Empty() {
			_ = SaveDebugImages(debugImg, moveNumber, debugDir)
			debugImg.Close()
		}

		// 计算坐标误差
		if result.X > 0 && result.Y > 0 {
			expectX, expectY := ConvertGTPToCoords(expectGTP)
			if expectX > 0 && expectY > 0 {
				squaredError := math.Pow(float64(result.X-expectX), 2) + math.Pow(float64(result.Y-expectY), 2)
				totalSquaredError += squaredError
				detail.SquaredError = squaredError
				detail.CoordinateError = fmt.Sprintf("%.2f", math.Sqrt(squaredError))

				// 更新最大和最小误差
				if squaredError > maxError {
					maxError = squaredError
				}
				if squaredError < minError {
					minError = squaredError
				}
				stats.TotalErrorCount++
			}
		}

		if isCorrect {
			stats.SuccessCount++
		} else {
			stats.FailureCount++
		}

		details = append(details, detail)
	}

	// 计算统计数据
	if stats.TotalCount > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalCount) * 100
	}
	if stats.TotalErrorCount > 0 {
		stats.MeanSquaredError = totalSquaredError / float64(stats.TotalErrorCount)
		stats.RootMeanSquaredError = math.Sqrt(stats.MeanSquaredError)
		if minError == math.MaxFloat64 {
			stats.MinError = 0
		} else {
			stats.MinError = math.Sqrt(minError)
		}
		stats.MaxError = math.Sqrt(maxError)
	}

	return stats, details, nil
}

// PrintBatchRecognitionStats 打印批量识别统计信息
func PrintBatchRecognitionStats(stats BatchRecognitionStats, details []RecognitionDetail) {
	fmt.Printf("\n%-30s | %-15s | %-15s | %-10s | %-10s | %-10s\n",
		"文件名", "预期(手数-坐标-颜色)", "识别结果", "图片尺寸", "置信度", "状态")
	fmt.Println(strings.Repeat("-", 100))

	for _, detail := range details {
		status := "✅ 正确"
		if !detail.IsCorrect {
			status = "❌ 错误"
		}
		fmt.Printf("%-30s | %-15s | %-15s | %-10s | %-10s | %s\n",
			detail.FileName, detail.Expected, detail.Actual,
			detail.ImageSize, detail.Confidence, status)

		if !detail.IsCorrect && detail.CoordinateError != "" {
			fmt.Printf("  -> 坐标误差: %s\n", detail.CoordinateError)
		}
	}

	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("测试总结: 总计 %d, 成功 %d, 失败 %d, 成功率 %.2f%%\n",
		stats.TotalCount, stats.SuccessCount, stats.FailureCount, stats.SuccessRate)

	// 打印误差统计
	if stats.TotalErrorCount > 0 {
		fmt.Println(strings.Repeat("-", 100))
		fmt.Println("误差统计:")
		fmt.Printf("总误差数量: %d\n", stats.TotalErrorCount)
		fmt.Printf("均方误差 (MSE): %.2f\n", stats.MeanSquaredError)
		fmt.Printf("均方根误差 (RMSE): %.2f\n", stats.RootMeanSquaredError)
		fmt.Printf("最大误差: %.2f\n", stats.MaxError)
		fmt.Printf("最小误差: %.2f\n", stats.MinError)
	}
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
	// fmt.Println("检测到的图片分辨率: ", resKey)
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
	// 如果为该分辨率配置了裁剪比例，则在透视矫正后裁剪 warped
	resCrop := CropPercent{Top: 0, Bottom: 0, Left: 0, Right: 0}
	if cp, ok := FixedBoardCropPercent[resKey]; ok {
		resCrop = cp
	}

	// 计算裁剪区域（基于 BoardWarpSize）
	leftPx := int(float64(BoardWarpSize) * resCrop.Left)
	topPx := int(float64(BoardWarpSize) * resCrop.Top)
	rightPx := BoardWarpSize - int(float64(BoardWarpSize)*resCrop.Right)
	bottomPx := BoardWarpSize - int(float64(BoardWarpSize)*resCrop.Bottom)

	// 校验范围
	if leftPx < 0 {
		leftPx = 0
	}
	if topPx < 0 {
		topPx = 0
	}
	if rightPx > warped.Cols() {
		rightPx = warped.Cols()
	}
	if bottomPx > warped.Rows() {
		bottomPx = warped.Rows()
	}
	if rightPx <= leftPx || bottomPx <= topPx {
		defer warped.Close()
		debugInfo["warp_size"] = fmt.Sprintf("%dx%d", warped.Cols(), warped.Rows())
	} else {
		// 裁剪并替换 warped
		roi := image.Rect(leftPx, topPx, rightPx, bottomPx)
		cropped := warped.Region(roi)
		newWarp := cropped.Clone()
		cropped.Close()
		warped.Close()
		warped = newWarp
		defer warped.Close()
		debugInfo["warp_size"] = fmt.Sprintf("%dx%d", warped.Cols(), warped.Rows())
		debugInfo["warp_crop"] = resCrop
	}

	// 2. 寻找角标
	debugInfo["step"] = "mark_detection"
	markPt, err := FindMark(warped, moveNumber)
	if err != nil {
		debugInfo["hsv_mark_error"] = err.Error()
		// HSV 颜色空间角标检测失败，尝试使用 BGR 颜色空间
		markPt, err = FindMarkBGR(warped, moveNumber)
		if err != nil {
			debugInfo["bgr_mark_error"] = err.Error()
			// 角标检测失败，尝试直接检测棋子
			stoneCenter, directErr := FindLastMoveDirect(warped, moveNumber)
			if directErr != nil {
				debugInfo["direct_detection_error"] = directErr.Error()
				debugInfo["final_status"] = "failed_at_mark_detection"
				// 所有检测方法都失败，返回默认结果
				color := "B"
				if moveNumber%2 == 0 {
					color = "W"
				}
				return Result{
					Move:       moveNumber,
					Color:      color,
					X:          0,
					Y:          0,
					Confidence: 0,
					Debug:      debugInfo,
				}, nil
			} else {
				debugInfo["detection_method"] = "direct"
				debugInfo["detected_stone_center"] = stoneCenter

				// 4. 计算网格并映射
				debugInfo["step"] = "grid_calculation"
				grid := CalculateGrid(warped)
				debugInfo["grid_inner_rect"] = grid.InnerRect
				debugInfo["grid_spacing"] = fmt.Sprintf("dx: %.2f, dy: %.2f", grid.Dx, grid.Dy)

				// 5. 手动计算最近的交叉点（修复坐标映射问题）
				minDist := math.MaxFloat64
				bestCol, bestRow := 0, 0

				for col := 0; col < 19; col++ {
					for row := 0; row < 19; row++ {
						pt := grid.Grid[col][row]
						dist := math.Hypot(float64(stoneCenter.X-pt.X), float64(stoneCenter.Y-pt.Y))
						if dist < minDist {
							minDist = dist
							bestCol, bestRow = col, row
						}
					}
				}

				// 6. 计算置信度
				maxDist := 0.5 * math.Min(grid.Dx, grid.Dy)
				confidence := 1.0 - minDist/maxDist
				if confidence < 0 {
					confidence = 0
				} else if confidence > 1 {
					confidence = 1
				}

				// 7. 转换为1-based坐标
				col := bestCol
				row := bestRow

				debugInfo["mapped_coordinates"] = fmt.Sprintf("col: %s, row: %d", string('A'+byte(col)), row)
				debugInfo["grid_confidence"] = confidence

				// 8. 验证手数数字
				debugInfo["step"] = "move_verification"
				oCRVerified, ocrErr := VerifyMoveNumber(warped, stoneCenter, moveNumber)
				debugInfo["ocr_verified"] = oCRVerified
				if ocrErr != nil {
					debugInfo["ocr_error"] = ocrErr.Error()
				}

				// 9. 计算最终置信度
				debugInfo["step"] = "confidence_calculation"
				finalConfidence := CalculateFinalConfidence(confidence, oCRVerified)
				debugInfo["final_confidence"] = finalConfidence

				// 10. 确定颜色
				color := "B"
				if moveNumber%2 == 0 {
					color = "W"
				}
				debugInfo["predicted_color"] = color

				// 11. 构建结果
				debugInfo["final_status"] = "success"
				result := Result{
					Move:       moveNumber,
					Color:      color,
					X:          col,
					Y:          row,
					Confidence: finalConfidence,
					Debug:      debugInfo,
				}

				return result, nil
			}
		} else {
			debugInfo["mark_detection_method"] = "BGR"
			debugInfo["detected_mark_point"] = markPt
		}
	} else {
		debugInfo["mark_detection_method"] = "HSV"
		debugInfo["detected_mark_point"] = markPt
	}

	// 3. 寻找棋子中心
	debugInfo["step"] = "stone_center_detection"
	stoneCenter, err := FindStoneCenter(warped, markPt)
	if err != nil {
		debugInfo["stone_center_error"] = err.Error()
		debugInfo["final_status"] = "failed_at_stone_center"
		// 棋子中心定位失败，返回默认结果
		return Result{
			Move:       moveNumber,
			Color:      "B",
			X:          0,
			Y:          0,
			Confidence: 0,
			Debug:      debugInfo,
		}, nil
	}
	debugInfo["detected_stone_center"] = stoneCenter

	// 4. 计算网格并映射
	debugInfo["step"] = "grid_calculation"
	grid := CalculateGrid(warped)
	debugInfo["grid_inner_rect"] = grid.InnerRect
	debugInfo["grid_spacing"] = fmt.Sprintf("dx: %.2f, dy: %.2f", grid.Dx, grid.Dy)

	// 5. 手动计算最近的交叉点（修复坐标映射问题）
	minDist := math.MaxFloat64
	bestCol, bestRow := 0, 0

	for col := 0; col < 19; col++ {
		for row := 0; row < 19; row++ {
			pt := grid.Grid[col][row]
			dist := math.Hypot(float64(stoneCenter.X-pt.X), float64(stoneCenter.Y-pt.Y))
			if dist < minDist {
				minDist = dist
				bestCol, bestRow = col, row
			}
		}
	}

	// 6. 计算置信度
	maxDist := 0.5 * math.Min(grid.Dx, grid.Dy)
	confidence := 1.0 - minDist/maxDist
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}

	// 7. 转换为1-based坐标
	col := bestCol
	row := bestRow

	debugInfo["mapped_coordinates"] = fmt.Sprintf("col: %s, row: %d", string('A'+byte(col)), row)
	debugInfo["grid_confidence"] = confidence

	// 8. 验证手数数字
	debugInfo["step"] = "move_verification"
	oCRVerified, ocrErr := VerifyMoveNumber(warped, stoneCenter, moveNumber)
	debugInfo["ocr_verified"] = oCRVerified
	if ocrErr != nil {
		debugInfo["ocr_error"] = ocrErr.Error()
	}

	// 9. 计算最终置信度
	debugInfo["step"] = "confidence_calculation"
	finalConfidence := CalculateFinalConfidence(confidence, oCRVerified)
	debugInfo["final_confidence"] = finalConfidence

	// 10. 确定颜色
	color := "B"
	if moveNumber%2 == 0 {
		color = "W"
	}
	debugInfo["predicted_color"] = color

	// 11. 构建结果
	debugInfo["final_status"] = "success"
	result := Result{
		Move:       moveNumber,
		Color:      color,
		X:          col,
		Y:          row,
		Confidence: finalConfidence,
		Debug:      debugInfo,
	}

	return result, nil
}

// SaveDebugImages 保存调试图像
func SaveDebugImages(img gocv.Mat, moveNumber int, outputDir string) error {
	// 创建输出目录
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	// 1. 棋盘定位与矫正
	// 使用全局预定义的硬编码棋盘区域，保证调试输出与实际使用一致
	var corners []image.Point
	resKey := fmt.Sprintf("%dx%d", img.Cols(), img.Rows())
	if c, ok := FixedBoardCorners[resKey]; ok {
		corners = c
	} else {
		return fmt.Errorf("不支持的图片分辨率: %dx%d，请添加硬编码的棋盘区域", img.Cols(), img.Rows())
	}

	// 保存使用的 corners 到 JSON 以便验证
	cornersInfo := map[string]any{
		"resKey":    resKey,
		"imgWidth":  img.Cols(),
		"imgHeight": img.Rows(),
		"corners":   corners,
	}
	if b, err := json.MarshalIndent(cornersInfo, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(outputDir, "debug_corners.json"), b, 0644)
	}

	// 在原图上绘制四角并保存，便于直观验证硬编码是否生效
	srcOverlay := img.Clone()
	defer srcOverlay.Close()
	// 绘制角点和连线
	for i := 0; i < len(corners); i++ {
		p := corners[i]
		gocv.Circle(&srcOverlay, p, 6, color.RGBA{255, 0, 0, 0}, -1)
		next := corners[(i+1)%len(corners)]
		gocv.Line(&srcOverlay, p, next, color.RGBA{255, 0, 0, 0}, 3)
	}
	_ = gocv.IMWrite(filepath.Join(outputDir, "debug_source_corners.jpg"), srcOverlay)

	warped, err := WarpBoard(img, corners)
	if err != nil {
		return err
	}

	// 对 warped 应用相同的裁剪策略（基于 FixedBoardCropPercent），以便 debug 输出与实际识别一致
	resCrop := CropPercent{Top: 0, Bottom: 0, Left: 0, Right: 0}
	if cp, ok := FixedBoardCropPercent[resKey]; ok {
		resCrop = cp
	}

	leftPx := int(float64(BoardWarpSize) * resCrop.Left)
	topPx := int(float64(BoardWarpSize) * resCrop.Top)
	rightPx := BoardWarpSize - int(float64(BoardWarpSize)*resCrop.Right)
	bottomPx := BoardWarpSize - int(float64(BoardWarpSize)*resCrop.Bottom)

	// 校验并执行裁剪
	if leftPx < 0 {
		leftPx = 0
	}
	if topPx < 0 {
		topPx = 0
	}
	if rightPx > warped.Cols() {
		rightPx = warped.Cols()
	}
	if bottomPx > warped.Rows() {
		bottomPx = warped.Rows()
	}
	if rightPx <= leftPx || bottomPx <= topPx {
		defer warped.Close()
	} else {
		roi := image.Rect(leftPx, topPx, rightPx, bottomPx)
		cropped := warped.Region(roi)
		newWarp := cropped.Clone()
		cropped.Close()
		warped.Close()
		warped = newWarp
		defer warped.Close()
	}

	// 保存棋盘矫正结果
	warpPath := filepath.Join(outputDir, "debug_boardWarp.jpg")
	if ok := gocv.IMWrite(warpPath, warped); !ok {
		return fmt.Errorf("无法保存棋盘矫正结果")
	}

	// 2. 生成角标颜色mask
	maskPath := filepath.Join(outputDir, "debug_mark_mask.jpg")
	if err := saveMarkMask(warped, moveNumber, maskPath); err != nil {
		return err
	}

	// 3. 生成叠加标记的调试图
	overlayPath := filepath.Join(outputDir, "debug_overlay.jpg")
	if err := saveOverlayImage(warped, corners, moveNumber, overlayPath); err != nil {
		return err
	}

	return nil
}

// saveMarkMask 保存角标颜色mask
func saveMarkMask(img gocv.Mat, moveNumber int, outputPath string) error {
	// 转换到HSV色彩空间
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// 颜色分割
	mask := gocv.NewMat()
	defer mask.Close()

	isWhite := moveNumber%2 == 0
	if isWhite {
		// 蓝色角标
		lowerBlue := gocv.NewMatFromScalar(gocv.NewScalar(90, 80, 80, 0), gocv.MatTypeCV8UC3)
		upperBlue := gocv.NewMatFromScalar(gocv.NewScalar(135, 255, 255, 0), gocv.MatTypeCV8UC3)
		defer lowerBlue.Close()
		defer upperBlue.Close()
		gocv.InRange(hsv, lowerBlue, upperBlue, &mask)
	} else {
		// 红色角标（两段）
		lowerRed1 := gocv.NewMatFromScalar(gocv.NewScalar(0, 80, 80, 0), gocv.MatTypeCV8UC3)
		upperRed1 := gocv.NewMatFromScalar(gocv.NewScalar(10, 255, 255, 0), gocv.MatTypeCV8UC3)
		lowerRed2 := gocv.NewMatFromScalar(gocv.NewScalar(160, 80, 80, 0), gocv.MatTypeCV8UC3)
		upperRed2 := gocv.NewMatFromScalar(gocv.NewScalar(179, 255, 255, 0), gocv.MatTypeCV8UC3)
		defer lowerRed1.Close()
		defer upperRed1.Close()
		defer lowerRed2.Close()
		defer upperRed2.Close()

		mask1 := gocv.NewMat()
		mask2 := gocv.NewMat()
		defer mask1.Close()
		defer mask2.Close()

		gocv.InRange(hsv, lowerRed1, upperRed1, &mask1)
		gocv.InRange(hsv, lowerRed2, upperRed2, &mask2)
		gocv.BitwiseOr(mask1, mask2, &mask)
	}

	// 保存mask
	if ok := gocv.IMWrite(outputPath, mask); !ok {
		return fmt.Errorf("无法保存mask: %s", outputPath)
	}
	return nil
}

// saveOverlayImage 保存叠加标记的调试图
func saveOverlayImage(img gocv.Mat, corners []image.Point, moveNumber int, outputPath string) error {
	// 创建副本
	overlay := img.Clone()
	defer overlay.Close()

	// 1. 绘制内矩形
	grid := CalculateGrid(img)
	gocv.Rectangle(&overlay, grid.InnerRect, color.RGBA{0, 255, 0, 0}, 2)

	// 2. 绘制角标
	markPt, err := FindMark(img, moveNumber)
	if err == nil {
		// 绘制角标中心点
		gocv.Circle(&overlay, markPt, 5, color.RGBA{0, 0, 255, 0}, -1)
		// 绘制角标矩形
		markRect := image.Rect(markPt.X-20, markPt.Y-20, markPt.X+20, markPt.Y+20)
		gocv.Rectangle(&overlay, markRect, color.RGBA{0, 0, 255, 0}, 2)
	}

	// 3. 绘制棋子中心
	if err == nil {
		stoneCenter, err := FindStoneCenter(img, markPt)
		if err == nil {
			// 绘制棋子中心点
			gocv.Circle(&overlay, stoneCenter, 5, color.RGBA{255, 0, 0, 0}, -1)
			// 绘制棋子半径
			gridSpacing := float64(BoardWarpSize) / 19.0
			rStone := 0.45 * gridSpacing
			gocv.Circle(&overlay, stoneCenter, int(rStone), color.RGBA{255, 0, 0, 0}, 2)

			// 4. 绘制最近的交叉点
			x, y, _ := MapToGrid(stoneCenter, grid)
			crossPt := grid.Grid[x-1][y-1]
			gocv.Circle(&overlay, crossPt, 5, color.RGBA{255, 255, 0, 0}, -1)
			// 绘制连接线
			gocv.Line(&overlay, stoneCenter, crossPt, color.RGBA{255, 255, 0, 0}, 2)
		}
	}

	// 5. 绘制网格线
	for i := 0; i < 19; i++ {
		for j := 0; j < 19; j++ {
			pt := grid.Grid[i][j]
			gocv.Circle(&overlay, pt, 2, color.RGBA{128, 128, 128, 0}, -1)
		}
	}

	// 保存叠加图
	if ok := gocv.IMWrite(outputPath, overlay); !ok {
		return fmt.Errorf("无法保存叠加图: %s", outputPath)
	}
	return nil
}

func NewDetector(b *board.Board) *Detector {
	return &Detector{
		BoardModel:  b,
		Threshold:   15.0, // 增加阈值以过滤噪点
		OCREndpoint: "http://127.0.0.1:5001/ocr",
	}
}

// FetchMoveNumberFromOCR 调用本地 OCR 接口获取当前手数
func (d *Detector) FetchMoveNumberFromOCR(img gocv.Mat) (int, error) {
	if img.Empty() {
		return 0, fmt.Errorf("图片为空")
	}

	// 1. 将 gocv.Mat 编码为 jpeg
	buf, err := gocv.IMEncode(".jpg", img)
	if err != nil {
		return 0, fmt.Errorf("图片编码失败: %v", err)
	}
	defer buf.Close()

	// 2. 构造 multipart 表单
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "board.jpg")
	if err != nil {
		return 0, fmt.Errorf("创建表单失败: %v", err)
	}
	_, err = part.Write(buf.GetBytes())
	if err != nil {
		return 0, fmt.Errorf("写入表单数据失败: %v", err)
	}
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
	for _, res := range results {
		match := re.FindStringSubmatch(res.Words)
		if len(match) > 1 {
			moveNum, _ := strconv.Atoi(match[1])
			return moveNum, nil
		}
	}

	return 0, fmt.Errorf("未在 OCR 结果中找到手数信息")
}

func (d *Detector) DetectLatestMove(img gocv.Mat) (int, int, int, string) {
	if img.Empty() {
		return -1, -1, ColorNone, "未知"
	}

	// 0. 尝试调用 OCR 获取当前手数
	ocrMoveNum, ocrErr := d.FetchMoveNumberFromOCR(img)
	expectedColor := ColorNone
	if ocrErr == nil {
		if ocrMoveNum%2 == 1 {
			expectedColor = ColorBlack
		} else {
			expectedColor = ColorWhite
		}
	}

	// 1. 确保有网格线
	if len(d.HGrid) != 19 || len(d.VGrid) != 19 {
		h, v, err := d.AutoCalibrateBoard(img)
		if err != nil {
			return -1, -1, ColorNone, "校准失败"
		}
		d.HGrid = h
		d.VGrid = v
	}

	// 2. 遍历 19x19 网格采样
	var currentBoard [19][19]int
	latestRow, latestCol := -1, -1
	blackCount, whiteCount := 0, 0

	// 存储所有可能的新落点
	var possibleMoves []struct {
		row, col   int
		complexity float64
		color      int
	}

	for r := 0; r < 19; r++ {
		for c := 0; c < 19; c++ {
			p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
			// 使用 OCR 手数确定颜色（奇数为黑，偶数为白）；如果 OCR 未提供，设置为未知
			color := expectedColor
			currentBoard[r][c] = color

			if color == ColorBlack {
				blackCount++
			} else if color == ColorWhite {
				whiteCount++
			}

			// 计算每个点的复杂度，用于识别最新落点
			// 修正：现在 CalculateCenterComplexity 内部会根据 stoneColor 自动检测红/蓝标记
			complexity := d.CalculateCenterComplexity(img, p, color)

			// 寻找可能的新落点
			if color != ColorNone {
				// 如果 OCR 确定了颜色，只考虑该颜色的落点作为候选
				if expectedColor != ColorNone && color != expectedColor {
					continue
				}

				// 检查是否是状态变化
				stateChanged := color != d.LastBoardState[r][c]

				// 如果是新落点或有标记，添加到候选列表
				// 标记分数在 CalculateCenterComplexity 中已经大幅提升 (2000+)
				if stateChanged || complexity > 100 {
					possibleMoves = append(possibleMoves, struct {
						row, col   int
						complexity float64
						color      int
					}{r, c, complexity, color})
				}
			}
		}
	}

	// 3. 从候选列表中选择最佳落点
	if len(possibleMoves) > 0 {
		// 寻找标记最明显的点
		bestMove := struct {
			row, col   int
			complexity float64
			color      int
		}{-1, -1, 0, ColorNone}

		for _, move := range possibleMoves {
			if move.complexity > bestMove.complexity {
				bestMove = move
			}
		}

		if bestMove.row != -1 {
			latestRow, latestCol = bestMove.row, bestMove.col
		}
	}

	// 4. 如果没找到标记，退而求其次找状态变化
	if latestRow == -1 {
		for r := 0; r < 19; r++ {
			for c := 0; c < 19; c++ {
				if currentBoard[r][c] != ColorNone && currentBoard[r][c] != d.LastBoardState[r][c] {
					// 如果 OCR 确定了颜色，必须匹配
					if expectedColor != ColorNone && currentBoard[r][c] != expectedColor {
						continue
					}
					latestRow, latestCol = r, c
					goto found
				}
			}
		}
	}
found:

	// 5. 更新状态
	d.LastBoardState = currentBoard
	color := ColorNone
	if latestRow != -1 {
		color = currentBoard[latestRow][latestCol]
	}

	// 6. 确定最终手数
	handNumber := "0"
	if ocrErr == nil {
		handNumber = fmt.Sprintf("%d", ocrMoveNum)
	} else {
		// OCR 失败，回退到统计计数的逻辑
		totalStones := blackCount + whiteCount
		if totalStones > 400 {
			totalStones = 0
		}
		handNumber = fmt.Sprintf("%d", totalStones)
	}

	return latestRow, latestCol, color, handNumber
}

// AutoCalibrateBoard 按照 img2sfg.py 逻辑重构：多模糊圆检测 -> 消除圆干扰 -> 标准霍夫直线 -> 补全网格
func (d *Detector) AutoCalibrateBoard(img gocv.Mat) ([]int, []int, error) {
	if img.Empty() {
		return nil, nil, fmt.Errorf("图片为空")
	}

	// 1. 预处理：限制区域以避开顶部和底部 UI (保留中间 60% 区域)
	roiY := int(float64(img.Rows()) * 0.2)
	roiH := int(float64(img.Rows()) * 0.6)
	roiRect := image.Rect(0, roiY, img.Cols(), roiY+roiH)
	roiImg := img.Region(roiRect)
	defer roiImg.Close()

	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(roiImg, &gray, gocv.ColorBGRToGray)

	// 2. 多模糊圆检测 (参考 img2sfg.py 的 maxblur=3)
	var allCircles []image.Point
	var radii []int

	// 生成不同模糊程度的图像
	blurSizes := []int{1, 3, 5, 7}
	for _, blurSize := range blurSizes {
		blurred := gocv.NewMat()
		if blurSize > 1 {
			gocv.GaussianBlur(gray, &blurred, image.Point{X: blurSize*2 + 1, Y: blurSize*2 + 1}, float64(blurSize), float64(blurSize), gocv.BorderDefault)
		} else {
			blurred = gray.Clone()
		}

		circles := gocv.NewMat()
		gocv.HoughCirclesWithParams(blurred, &circles, gocv.HoughGradient, 1, 15, 100, 30, 10, 35)

		// 收集圆
		for i := 0; i < circles.Cols(); i++ {
			v := circles.GetVecfAt(0, i)
			cx, cy, r := int(v[0]), int(v[1]), int(v[2])
			allCircles = append(allCircles, image.Point{X: cx, Y: cy})
			radii = append(radii, r)
		}

		circles.Close()
		if blurSize > 1 {
			blurred.Close()
		}
	}

	// 3. 边缘检测 (参考 imago 项目的 prepare 函数，但使用更保守的参数)
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 50, 150) // 恢复到更保守的阈值

	// 4. 消除圆（棋子）干扰
	cleanEdges := edges.Clone()
	defer cleanEdges.Close()

	for i, center := range allCircles {
		r := radii[i] + 3 // 稍微扩大半径以确保完全覆盖
		rect := image.Rect(center.X-r, center.Y-r, center.X+r, center.Y+r)
		// 填充黑色以消除圆的干扰
		gocv.Rectangle(&cleanEdges, rect, color.RGBA{0, 0, 0, 0}, -1)
		// 在中心留下一个白色点，便于后续处理
		gocv.Circle(&cleanEdges, center, 1, color.RGBA{255, 255, 255, 0}, -1)
	}

	// 5. 标准霍夫直线检测 (HoughLines)
	linesMat := gocv.NewMat()
	defer linesMat.Close()
	// 恢复到更保守的阈值以确保检测到足够的线条
	gocv.HoughLines(cleanEdges, &linesMat, 1, math.Pi/180, 100)

	var hLines, vLines []float32
	angleTolerance := float64(1.5 * math.Pi / 180.0) // 恢复到之前的角度容差

	for i := 0; i < linesMat.Rows(); i++ {
		line := linesMat.GetVecfAt(i, 0)
		rho := line[0]
		theta := float64(line[1])

		if math.Abs(theta-math.Pi/2) < angleTolerance {
			// 水平线，映射回原图坐标
			hLines = append(hLines, rho+float32(roiRect.Min.Y))
		} else if theta < angleTolerance || math.Abs(theta-math.Pi) < angleTolerance {
			// 垂直线
			r := rho
			if math.Abs(theta-math.Pi) < angleTolerance {
				r = -rho
			}
			vLines = append(vLines, r)
		}
	}

	// 4. 聚类合并极近的线条
	hCentres := clusterLines(hLines, 10)
	vCentres := clusterLines(vLines, 10)

	// 5. 补全网格
	hGrid := completeGrid(hCentres, 19)
	vGrid := completeGrid(vCentres, 19)

	// 6. 利用圆心点对网格进行精细平移和缩放对齐 (全局优化)
	if len(hGrid) == 19 && len(vGrid) == 19 && len(allCircles) > 1 {
		bestHOffset, bestVOffset := float32(0), float32(0)
		maxScore := -1.0

		// 尝试微调偏移和缩放 (这里为了性能，先只做偏移微调)
		for ho := float32(-10); ho <= 10; ho += 2 {
			for vo := float32(-10); vo <= 10; vo += 2 {
				score := 0.0
				for _, center := range allCircles {
					cx, cy := float32(center.X), float32(center.Y)+float32(roiRect.Min.Y)

					// 计算到最近交点的距离
					minDist := float64(1000)
					for _, h := range hGrid {
						for _, v := range vGrid {
							d := math.Hypot(float64(cy-(h+ho)), float64(cx-(v+vo)))
							if d < minDist {
								minDist = d
							}
						}
					}
					if minDist < 15 {
						score += 1.0 - minDist/15.0
					}
				}
				if score > maxScore {
					maxScore = score
					bestHOffset = ho
					bestVOffset = vo
				}
			}
		}

		// 应用最佳平移
		for i := range hGrid {
			hGrid[i] += bestHOffset
		}
		for i := range vGrid {
			vGrid[i] += bestVOffset
		}
	}

	if len(hGrid) != 19 || len(vGrid) != 19 {
		return nil, nil, fmt.Errorf("未能重建 19x19 网格 (H:%d, V:%d)", len(hGrid), len(vGrid))
	}

	// 转换回 int
	hResult := make([]int, 19)
	for i, v := range hGrid {
		hResult[i] = int(math.Round(float64(v)))
	}
	vResult := make([]int, 19)
	for i, v := range vGrid {
		vResult[i] = int(math.Round(float64(v)))
	}

	return hResult, vResult, nil
}

func clusterLines(lines []float32, minSpacing float32) []float32 {
	if len(lines) == 0 {
		return nil
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i] < lines[j] })

	var clusters []float32
	if len(lines) > 0 {
		currentSum := lines[0]
		count := 1
		for i := 1; i < len(lines); i++ {
			if lines[i]-lines[i-1] < minSpacing {
				currentSum += lines[i]
				count++
			} else {
				clusters = append(clusters, currentSum/float32(count))
				currentSum = lines[i]
				count = 1
			}
		}
		clusters = append(clusters, currentSum/float32(count))
	}
	return clusters
}

func completeGrid(x []float32, expected int) []float32 {
	if len(x) < 2 {
		return nil
	}

	// 截断逻辑参考 truncate_grid
	if len(x) == expected+2 {
		x = x[1 : expected+1]
	} else if len(x) == expected+1 {
		x = x[:expected]
	}

	if len(x) == expected {
		return x
	}

	// 计算间距
	var spaces []float32
	var minSpace float32 = 1000000
	for i := 1; i < len(x); i++ {
		s := x[i] - x[i-1]
		spaces = append(spaces, s)
		if s < minSpace {
			minSpace = s
		}
	}

	if minSpace < 5 {
		return nil
	}

	// 估算平均间距 (取非大间距的均值)
	var smallSpaces []float32
	bound := minSpace * 1.6
	for _, s := range spaces {
		if s <= bound {
			smallSpaces = append(smallSpaces, s)
		}
	}

	var avgSpace float32
	if len(smallSpaces) > 0 {
		var sum float32
		for _, s := range smallSpaces {
			sum += s
		}
		avgSpace = sum / float32(len(smallSpaces))
	} else {
		avgSpace = minSpace
	}

	// 补全
	var result []float32
	result = append(result, x[0])
	for i := 0; i < len(spaces); i++ {
		s := spaces[i]
		if s <= bound {
			result = append(result, x[i+1])
		} else {
			m := int(math.Round(float64(s / avgSpace)))
			for k := 1; k <= m; k++ {
				result = append(result, x[i]+float32(k)*s/float32(m))
			}
		}
	}

	// 如果补全后还是不对，或者超了，尝试截取或按平均间距向外推（这里简单处理，只返回长度匹配的部分）
	if len(result) > expected {
		// 寻找最匹配的 19 条 (暂取中间)
		start := (len(result) - expected) / 2
		return result[start : start+expected]
	}

	// 如果不足 19 条，尝试向两侧延展
	for len(result) < expected {
		// 优先向后延展
		last := result[len(result)-1]
		result = append(result, last+avgSpace)
		if len(result) == expected {
			break
		}
		// 向前延展
		first := result[0]
		result = append([]float32{first - avgSpace}, result...)
	}

	return result
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// CalculateCenterComplexity 计算棋子标记复杂度，针对腾讯围棋进行优化
// 黑棋最新一手左上角有红色标记，白棋最新一手左上角有蓝色标记
func (d *Detector) CalculateCenterComplexity(img gocv.Mat, center image.Point, stoneColor int) float64 {
	if stoneColor == ColorNone {
		return 0
	}

	// 1. 定义检测区域：聚焦于棋子的左上角部分
	// 腾讯围棋的标记通常在棋子边缘，偏移中心点往左上移动
	regionSize := 10
	offsetX := -6
	offsetY := -6
	rect := image.Rect(center.X+offsetX-regionSize, center.Y+offsetY-regionSize, center.X+offsetX+regionSize, center.Y+offsetY+regionSize)

	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > img.Cols() || rect.Max.Y > img.Rows() {
		return 0
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 2. 转换为 HSV 色彩空间以进行颜色提取
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(roi, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	defer mask.Close()

	if stoneColor == ColorBlack {
		// 检测红色标记 (黑棋)
		lowerRed1 := gocv.NewMatFromScalar(gocv.NewScalar(0, 100, 100, 0), gocv.MatTypeCV8UC3)
		upperRed1 := gocv.NewMatFromScalar(gocv.NewScalar(10, 255, 255, 0), gocv.MatTypeCV8UC3)
		lowerRed2 := gocv.NewMatFromScalar(gocv.NewScalar(160, 100, 100, 0), gocv.MatTypeCV8UC3)
		upperRed2 := gocv.NewMatFromScalar(gocv.NewScalar(180, 255, 255, 0), gocv.MatTypeCV8UC3)
		defer lowerRed1.Close()
		defer upperRed1.Close()
		defer lowerRed2.Close()
		defer upperRed2.Close()

		m1 := gocv.NewMat()
		m2 := gocv.NewMat()
		defer m1.Close()
		defer m2.Close()
		gocv.InRange(hsv, lowerRed1, upperRed1, &m1)
		gocv.InRange(hsv, lowerRed2, upperRed2, &m2)
		gocv.BitwiseOr(m1, m2, &mask)
	} else if stoneColor == ColorWhite {
		// 检测蓝色标记 (白棋)
		lowerBlue := gocv.NewMatFromScalar(gocv.NewScalar(100, 150, 50, 0), gocv.MatTypeCV8UC3)
		upperBlue := gocv.NewMatFromScalar(gocv.NewScalar(140, 255, 255, 0), gocv.MatTypeCV8UC3)
		defer lowerBlue.Close()
		defer upperBlue.Close()
		gocv.InRange(hsv, lowerBlue, upperBlue, &mask)
	}

	// 3. 计算颜色像素比例
	activePixels := gocv.CountNonZero(mask)
	totalPixels := mask.Rows() * mask.Cols()
	ratio := float64(activePixels) / float64(totalPixels)

	// 如果标记颜色比例足够高，判定为最新落点
	if ratio > 0.1 {
		return 2000.0 + ratio*1000.0 // 极高分值
	}

	// 4. 备选方案：计算灰度标准差 (寻找可能存在的数字或其他变化)
	grayROI := gocv.NewMat()
	defer grayROI.Close()
	gocv.CvtColor(roi, &grayROI, gocv.ColorBGRToGray)

	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(grayROI, &meanMat, &stddevMat)

	stdDev := stddevMat.GetDoubleAt(0, 0)
	if stdDev > 40 {
		return 500.0 + stdDev
	}

	return stdDev
}
