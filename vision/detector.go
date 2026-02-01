package vision

import (
	"fmt"
	"image"
	"image/color"
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
	BoardModel    *board.Board
	LastBoardState [19][19]int // 存储上一次识别的 19x19 状态
	Threshold     float64
	HGrid         []int // 19 条水平线坐标
	VGrid         []int // 19 条垂直线坐标
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
	maxComplexity := 0.0
	blackCount, whiteCount := 0, 0

	for r := 0; r < 19; r++ {
		for c := 0; c < 19; c++ {
			p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
			color := d.AnalyzeStoneColor(img, p, r, c)
			currentBoard[r][c] = color

			if color == ColorBlack {
				blackCount++
			} else if color == ColorWhite {
				whiteCount++
			}

			// 寻找新落点：状态变化且复杂度（标记）最高
			if color != ColorNone && color != d.LastBoardState[r][c] {
				complexity := d.CalculateCenterComplexity(img, p, color)
				if complexity > maxComplexity {
					maxComplexity = complexity
					latestRow, latestCol = r, c
				}
			}
		}
	}

	// 如果没有通过复杂度找到（可能标记不明显），则取任意一个状态变化的棋子
	if latestRow == -1 {
		for r := 0; r < 19; r++ {
			for c := 0; c < 19; c++ {
				if currentBoard[r][c] != ColorNone && currentBoard[r][c] != d.LastBoardState[r][c] {
					latestRow, latestCol = r, c
					goto found
				}
			}
		}
	}
found:

	// 更新状态
	d.LastBoardState = currentBoard
	color := ColorNone
	if latestRow != -1 {
		color = currentBoard[latestRow][latestCol]
	}

	handNumber := fmt.Sprintf("%d", blackCount+whiteCount)
	return latestRow, latestCol, color, handNumber
}

// AutoCalibrateBoard 按照 img2sfg.py 逻辑重构：消除圆干扰 -> 标准霍夫直线 -> 补全网格
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

	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{X: 5, Y: 5}, 0, 0, gocv.BorderDefault)

	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 50, 150)

	// 2. 检测并消除圆（棋子）干扰
	circles := gocv.NewMat()
	defer circles.Close()
	gocv.HoughCirclesWithParams(blurred, &circles, gocv.HoughGradient, 1, 15, 100, 30, 10, 35)

	cleanEdges := edges.Clone()
	defer cleanEdges.Close()

	for i := 0; i < circles.Cols(); i++ {
		v := circles.GetVecfAt(0, i)
		cx, cy, r := int(v[0]), int(v[1]), int(v[2])
		rr := r + 3
		rect := image.Rect(cx-rr, cy-rr, cx+rr, cy+rr)
		gocv.Rectangle(&cleanEdges, rect, color.RGBA{0, 0, 0, 0}, -1)
		gocv.Circle(&cleanEdges, image.Point{X: cx, Y: cy}, 1, color.RGBA{255, 255, 255, 0}, -1)
	}

	// 3. 标准霍夫直线检测 (HoughLines)
	linesMat := gocv.NewMat()
	defer linesMat.Close()
	// 增加 threshold 到 100 以过滤杂波
	gocv.HoughLines(cleanEdges, &linesMat, 1, math.Pi/180, 100)

	var hLines, vLines []float32
	angleTolerance := float64(1.5 * math.Pi / 180.0) // 稍微放宽角度容差

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
	if len(hGrid) == 19 && len(vGrid) == 19 && circles.Cols() > 1 {
		bestHOffset, bestVOffset := float32(0), float32(0)
		maxScore := -1.0

		// 尝试微调偏移和缩放 (这里为了性能，先只做偏移微调)
		for ho := float32(-10); ho <= 10; ho += 2 {
			for vo := float32(-10); vo <= 10; vo += 2 {
				score := 0.0
				for i := 0; i < circles.Cols(); i++ {
					vec := circles.GetVecfAt(0, i)
					cx, cy := vec[0], vec[1]+float32(roiRect.Min.Y)

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


func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// CalculateCenterComplexity 计算棋子中心的复杂度，专门寻找腾讯围棋的红色最新落点标记
func (d *Detector) CalculateCenterComplexity(img gocv.Mat, center image.Point, stoneColor int) float64 {
	if stoneColor == ColorNone {
		return 0
	}

	regionSize := 8
	rect := image.Rect(center.X-regionSize, center.Y-regionSize, center.X+regionSize, center.Y+regionSize)
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > img.Cols() || rect.Max.Y > img.Rows() {
		return 0
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 1. 检测红色分量 (腾讯围棋最新落点通常有红色标记)
	// BGR 格式，红色是 index 2
	meanBGR := roi.Mean()
	rVal := meanBGR.Val3
	gVal := meanBGR.Val2
	bVal := meanBGR.Val1

	// 如果红色显著高于绿和蓝，则非常有可能是最新落点标记
	redness := rVal - (gVal+bVal)/2.0
	if redness > 20 {
		return 1000.0 + redness // 给一个极大的基础分
	}

	// 2. 备选方案：计算灰度标准差 (寻找数字等标记)
	grayROI := gocv.NewMat()
	defer grayROI.Close()
	gocv.CvtColor(roi, &grayROI, gocv.ColorBGRToGray)

	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(grayROI, &meanMat, &stddevMat)

	return stddevMat.GetDoubleAt(0, 0)
}

// AnalyzeStoneColor 分析棋子颜色 (采样网格交叉点中心区域)
func (d *Detector) AnalyzeStoneColor(img gocv.Mat, p image.Point, r, c int) int {
	// 计算采样半径：网格间距的 1/3 (更集中于棋子中心，避开邻近棋子)
	var hSpace, vSpace int
	if r < 18 {
		hSpace = d.HGrid[r+1] - d.HGrid[r]
	} else {
		hSpace = d.HGrid[r] - d.HGrid[r-1]
	}
	if c < 18 {
		vSpace = d.VGrid[c+1] - d.VGrid[c]
	} else {
		vSpace = d.VGrid[c] - d.VGrid[c-1]
	}

	size := int(math.Min(float64(hSpace), float64(vSpace)) / 3.0)
	if size < 4 {
		size = 4
	}

	rect := image.Rect(p.X-size/2, p.Y-size/2, p.X+size/2, p.Y+size/2)
	// 边界检查
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > img.Cols() {
		rect.Max.X = img.Cols()
	}
	if rect.Max.Y > img.Rows() {
		rect.Max.Y = img.Rows()
	}

	if rect.Empty() || rect.Dx() < 2 || rect.Dy() < 2 {
		return ColorNone
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 计算均值和标准差
	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(roi, &meanMat, &stddevMat)

	// 简单取 BGR 均值之和的平均作为亮度
	avgBrightness := (meanMat.GetDoubleAt(0, 0) + meanMat.GetDoubleAt(1, 0) + meanMat.GetDoubleAt(2, 0)) / 3.0

	// 棋子判断逻辑：
	// 1. 颜色鲜艳度判断 (棋子应为灰色/黑/白，BGR 差异小)
	b, g, rv := meanMat.GetDoubleAt(0, 0), meanMat.GetDoubleAt(1, 0), meanMat.GetDoubleAt(2, 0)
	// 腾讯围棋背景色通常是 R:200+, G:150+, B:80+ (偏黄)
	// 增加对背景色的过滤：如果 R > G > B 且差值显著，则为空位
	if rv > g+10 && g > b+10 {
		return ColorNone
	}

	isGray := math.Abs(b-g) < 20 && math.Abs(g-rv) < 20 && math.Abs(b-rv) < 20

	if !isGray {
		return ColorNone
	}

	// 2. 亮度阈值
	if avgBrightness < 95 {
		return ColorBlack
	} else if avgBrightness > 175 {
		return ColorWhite
	}
	return ColorNone
}
