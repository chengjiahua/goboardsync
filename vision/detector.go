package vision

import (
	"fmt"
	"image"
	"math"
	"my-app/board"
	"sort"

	"gocv.io/x/gocv"
)

const (
	ColorNone  = 0
	ColorBlack = 1
	ColorWhite = 2
)

type Detector struct {
	BoardModel *board.Board
	LastState  [19][19]float64
	Threshold  float64
	HGrid      []int // 19 条水平线坐标
	VGrid      []int // 19 条垂直线坐标
}

func NewDetector(b *board.Board) *Detector {
	return &Detector{
		BoardModel: b,
		Threshold:  15.0, // 增加阈值以过滤噪点
	}
}

// DetectMove 识别落点
// 返回值: row, col
func (d *Detector) DetectMove(img gocv.Mat) (int, int) {
	row, col, _, _ := d.DetectLatestMove(img)
	return row, col
}

func (d *Detector) DetectLatestMove(img gocv.Mat) (int, int, int, string) {
	if img.Empty() {
		return -1, -1, ColorNone, "未知"
	}

	// 1. 确定 ROI
	var boardRect image.Rectangle
	if len(d.HGrid) == 19 && len(d.VGrid) == 19 {
		// 使用检测到的网格线作为 ROI 基础，稍微向外扩展一点
		boardRect = image.Rect(d.VGrid[0]-20, d.HGrid[0]-20, d.VGrid[18]+20, d.HGrid[18]+20)
	} else {
		// 回退到 BoardModel
		boardRect = image.Rect(d.BoardModel.TopLeft.X-10, d.BoardModel.TopLeft.Y-10, d.BoardModel.BottomRight.X+10, d.BoardModel.BottomRight.Y+10)
	}

	if boardRect.Min.X < 0 {
		boardRect.Min.X = 0
	}
	if boardRect.Min.Y < 0 {
		boardRect.Min.Y = 0
	}
	if boardRect.Max.X > img.Cols() {
		boardRect.Max.X = img.Cols()
	}
	if boardRect.Max.Y > img.Rows() {
		boardRect.Max.Y = img.Rows()
	}

	roiImg := img.Region(boardRect)
	defer roiImg.Close()

	// 2. 预处理
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(roiImg, &gray, gocv.ColorBGRToGray)

	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: 9, Y: 9}, 2, 2, gocv.BorderDefault)

	// 3. 检测圆
	circles := gocv.NewMat()
	defer circles.Close()
	gocv.HoughCirclesWithParams(blurred, &circles, gocv.HoughGradient, 1, float64(roiImg.Rows()/25), 100, 38, 22, 45)

	latestRow, latestCol, latestColor := -1, -1, ColorNone
	blackCount, whiteCount := 0, 0
	maxStdDev := 0.0

	for i := 0; i < circles.Cols(); i++ {
		v := circles.GetVecfAt(0, i)
		xRel, yRel := int(v[0]), int(v[1])
		xAbs, yAbs := xRel+boardRect.Min.X, yRel+boardRect.Min.Y

		// 映射坐标
		var r, c int
		if len(d.HGrid) == 19 && len(d.VGrid) == 19 {
			r = findClosestIndex(yAbs, d.HGrid)
			c = findClosestIndex(xAbs, d.VGrid)
		} else {
			r, c = d.BoardModel.GetGoCoordinate(image.Point{X: xAbs, Y: yAbs})
		}

		if r < 0 || r > 18 || c < 0 || c > 18 {
			continue
		}

		color := d.AnalyzeStoneColor(img, image.Point{X: xAbs, Y: yAbs})
		if color == ColorBlack {
			blackCount++
		} else if color == ColorWhite {
			whiteCount++
		}

		stdDev := d.CalculateCenterComplexity(img, image.Point{X: xAbs, Y: yAbs}, color)
		if stdDev > maxStdDev && stdDev > 8.0 { // 稍微降低阈值
			maxStdDev = stdDev
			latestRow, latestCol, latestColor = r, c, color
		}
	}

	handNumber := fmt.Sprintf("%d", blackCount+whiteCount)
	return latestRow, latestCol, latestColor, handNumber
}

func findClosestIndex(val int, sortedList []int) int {
	idx := sort.SearchInts(sortedList, val)
	if idx == 0 {
		return 0
	}
	if idx == len(sortedList) {
		return len(sortedList) - 1
	}
	if val-sortedList[idx-1] < sortedList[idx]-val {
		return idx - 1
	}
	return idx
}

// AutoCalibrateBoard 自动检测棋盘边界并返回 19x19 的网格线坐标
func (d *Detector) AutoCalibrateBoard(img gocv.Mat) ([]int, []int, error) {
	if img.Empty() {
		return nil, nil, fmt.Errorf("图片为空")
	}

	// 1. 预处理：限制区域以避开 UI 干扰 (顶部和底部各留 20% 空间)
	roiRect := image.Rect(0, int(float64(img.Rows())*0.2), img.Cols(), int(float64(img.Rows())*0.8))
	roiImg := img.Region(roiRect)
	defer roiImg.Close()

	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(roiImg, &gray, gocv.ColorBGRToGray)

	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 50, 150)

	// 2. 概率霍夫直线检测
	lines := gocv.NewMat()
	defer lines.Close()
	// 寻找长度至少为宽度的 50% 的线段
	gocv.HoughLinesPWithParams(edges, &lines, 1, 3.14159/180, 80, float32(img.Cols())*0.5, 20)

	var hLines, vLines []int
	for i := 0; i < lines.Rows(); i++ {
		line := lines.GetVeciAt(i, 0)
		x1, y1, x2, y2 := int(line[0]), int(line[1]), int(line[2]), int(line[3])

		// 转换回原图坐标
		y1 += roiRect.Min.Y
		y2 += roiRect.Min.Y

		if abs(y1-y2) < 10 { // 水平线
			hLines = append(hLines, (y1+y2)/2)
		}
		if abs(x1-x2) < 10 { // 垂直线
			vLines = append(vLines, (x1+x2)/2)
		}
	}

	// 3. 聚类并外推 19 条网格线
	hGrid := extrapolateGridLines(hLines, 19)
	vGrid := extrapolateGridLines(vLines, 19)

	if len(hGrid) < 19 || len(vGrid) < 19 {
		return nil, nil, fmt.Errorf("未能重建 19x19 棋盘网格 (H:%d, V:%d)", len(hGrid), len(vGrid))
	}

	return hGrid, vGrid, nil
}

func extrapolateGridLines(lines []int, expectedCount int) []int {
	if len(lines) < 2 {
		return nil
	}

	sort.Ints(lines)

	// 1. 聚类
	var clusters []int
	currentCluster := lines[0]
	count := 1
	for i := 1; i < len(lines); i++ {
		if lines[i]-lines[i-1] < 10 {
			currentCluster += lines[i]
			count++
		} else {
			clusters = append(clusters, currentCluster/count)
			currentCluster = lines[i]
			count = 1
		}
	}
	clusters = append(clusters, currentCluster/count)

	if len(clusters) < 5 { // 至少需要几条线来估算间距
		return nil
	}

	// 2. 计算最常见的间距 (Mode of gaps)
	var gaps []int
	for i := 1; i < len(clusters); i++ {
		gaps = append(gaps, clusters[i]-clusters[i-1])
	}
	sort.Ints(gaps)

	// 取中间值作为预估间距
	avgGap := gaps[len(gaps)/2]
	if avgGap < 10 {
		return nil
	}

	// 3. 寻找最长的连续等间距子集
	// 这里简化处理：假设棋盘主体已被检测到，我们向两侧扩展直到满 19 条
	// 寻找符合 avgGap 的最大簇
	// 实际生产中可以使用更复杂的 RANSAC 算法

	// 我们假设 clusters 中的线都是网格线，只是有缺失
	// 找到起始线和终止线
	minVal := clusters[0]
	maxVal := clusters[len(clusters)-1]

	totalSpan := maxVal - minVal
	estimatedCount := int(math.Round(float64(totalSpan)/float64(avgGap))) + 1

	if estimatedCount > expectedCount+2 {
		// 线条太多，可能包含边框，尝试截断
		// 暂时只取中间 19 条
	}

	// 重建完整的网格：根据平均间距和现有线的位置进行对齐
	// 我们取最中间的一条线作为基准点
	baseLine := clusters[len(clusters)/2]

	var grid []int
	// 寻找相对于 baseLine 的偏移量，使得尽可能多的现有线落在格点上
	// 这里简化为：直接以最外围的 19 条线范围为准
	// 修正逻辑：既然我们要 19 条线，我们就在 clusters 覆盖的范围内寻找最合理的 19 条

	// 尝试寻找最左边的线（第 0 条）
	// 我们知道 baseLine 是第 N 条线，则第 0 条线在 baseLine - N * avgGap
	// 我们尝试不同的 N 来最大化匹配度
	bestOffset := 0
	maxMatches := 0

	for n := 0; n < expectedCount; n++ {
		startLine := baseLine - n*avgGap
		matches := 0
		for _, c := range clusters {
			dist := abs(c - startLine)
			if dist%avgGap < 5 || dist%avgGap > avgGap-5 {
				matches++
			}
		}
		if matches > maxMatches {
			maxMatches = matches
			bestOffset = startLine
		}
	}

	for i := 0; i < expectedCount; i++ {
		grid = append(grid, bestOffset+i*avgGap)
	}

	return grid
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// CalculateCenterComplexity 计算棋子中心的标准差以识别标记 (使用灰度图以消除颜色偏见)
func (d *Detector) CalculateCenterComplexity(img gocv.Mat, center image.Point, stoneColor int) float64 {
	if stoneColor == ColorNone {
		return 0
	}

	regionSize := 6
	rect := image.Rect(center.X-regionSize, center.Y-regionSize, center.X+regionSize, center.Y+regionSize)
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > img.Cols() || rect.Max.Y > img.Rows() {
		return 0
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 转换为灰度图计算
	grayROI := gocv.NewMat()
	defer grayROI.Close()
	gocv.CvtColor(roi, &grayROI, gocv.ColorBGRToGray)

	mean := gocv.NewMat()
	defer mean.Close()
	stddev := gocv.NewMat()
	defer stddev.Close()
	gocv.MeanStdDev(grayROI, &mean, &stddev)

	return stddev.GetDoubleAt(0, 0)
}

// AnalyzeStoneColor 分析棋子颜色 (采样圆环区域以避开中心数字标记)
func (d *Detector) AnalyzeStoneColor(img gocv.Mat, p image.Point) int {
	// 定义内部和外部半径，形成一个圆环
	innerR := 10
	outerR := 18

	// 采样多个点
	var intensities []float64
	for angle := 0.0; angle < 360.0; angle += 45.0 {
		rad := angle * 3.14159 / 180.0
		// 在内圆和外圆之间采样
		for r := innerR; r <= outerR; r += 4 {
			px := p.X + int(float64(r)*math.Cos(rad))
			py := p.Y + int(float64(r)*math.Sin(rad))

			if px >= 0 && py >= 0 && px < img.Cols() && py < img.Rows() {
				bgr := img.GetVecbAt(py, px)
				// BGR -> Gray
				gray := 0.299*float64(bgr[2]) + 0.587*float64(bgr[1]) + 0.114*float64(bgr[0])
				intensities = append(intensities, gray)
			}
		}
	}

	if len(intensities) == 0 {
		return ColorNone
	}

	// 计算平均亮度
	sum := 0.0
	for _, v := range intensities {
		sum += v
	}
	avg := sum / float64(len(intensities))

	// 真正的棋子在圆环区域应保持纯色
	// 腾讯围棋：黑色棋子平均亮度较低，白色棋子极高
	if avg < 80 {
		return ColorBlack
	} else if avg > 180 {
		return ColorWhite
	}
	return ColorNone
}

// HasMarker 检查棋子中心是否有标记（数字或圆圈导致的亮度/标准差突变）
func (d *Detector) HasMarker(img gocv.Mat, center image.Point, stoneColor int) bool {
	if stoneColor == ColorNone {
		return false
	}

	regionSize := 6
	rect := image.Rect(center.X-regionSize, center.Y-regionSize, center.X+regionSize, center.Y+regionSize)
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > img.Cols() || rect.Max.Y > img.Rows() {
		return false
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 计算标准差来检测文字/标记
	mean := gocv.NewMat()
	defer mean.Close()
	stddev := gocv.NewMat()
	defer stddev.Close()
	gocv.MeanStdDev(roi, &mean, &stddev)

	// 从 stddev Mat 中提取值 (假设是灰度或均值)
	// 如果是彩色图，stddev 会有多个通道
	s := stddev.GetDoubleAt(0, 0)

	return s > 25.0
}
