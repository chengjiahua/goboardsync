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
	"regexp"
	"sort"
	"strconv"
	"time"

	"gocv.io/x/gocv"
)

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
			color := d.AnalyzeStoneColor(img, p, r, c)
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

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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

// AnalyzeStoneColor 分析棋子颜色 (参考 img2sfg.py 的 average_intensity 函数)
func (d *Detector) AnalyzeStoneColor(img gocv.Mat, p image.Point, r, c int) int {
	// 计算采样半径：网格间距的 1/3 (更集中于棋子中心)
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

	// 使用网格间距的 1/3 作为采样区域
	sampleSize := int(math.Min(float64(hSpace), float64(vSpace)) / 3.0)
	if sampleSize < 4 {
		sampleSize = 4
	}

	// 计算采样区域
	rect := image.Rect(p.X-sampleSize, p.Y-sampleSize, p.X+sampleSize, p.Y+sampleSize)
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

	// 采样原始颜色区域
	colorRoi := img.Region(rect)
	defer colorRoi.Close()

	// 计算颜色均值和标准差
	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(colorRoi, &meanMat, &stddevMat)

	// 获取 BGR 均值
	bVal := meanMat.GetDoubleAt(0, 0)
	gVal := meanMat.GetDoubleAt(1, 0)
	rVal := meanMat.GetDoubleAt(2, 0)

	// 计算亮度
	brightness := (bVal + gVal + rVal) / 3.0

	// 计算颜色鲜艳度
	maxRGB := math.Max(math.Max(bVal, gVal), rVal)
	minRGB := math.Min(math.Min(bVal, gVal), rVal)
	colorRange := maxRGB - minRGB

	// 1. 过滤背景色：背景通常有特定的颜色分布
	// 腾讯围棋背景色通常是 R:200+, G:150+, B:80+ (偏黄)
	if rVal > 180 && gVal > 130 && bVal > 60 && rVal > gVal+20 && gVal > bVal+20 {
		return ColorNone
	}

	// 2. 过滤颜色鲜艳度：棋子应为灰色/黑/白，BGR 差异小
	if colorRange > 30 {
		return ColorNone
	}

	// 3. 基于亮度的石头分类
	// 调整阈值以提高准确性
	if brightness < 90 {
		// 黑色石头
		return ColorBlack
	} else if brightness > 150 {
		// 白色石头
		return ColorWhite
	}

	return ColorNone
}

// AnalyzeStoneColorRelaxed 使用更宽松的阈值分析棋子颜色
func (d *Detector) AnalyzeStoneColorRelaxed(img gocv.Mat, p image.Point, r, c int) int {
	// 计算采样半径：网格间距的 1/3 (更集中于棋子中心)
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

	// 使用网格间距的 1/3 作为采样区域
	sampleSize := int(math.Min(float64(hSpace), float64(vSpace)) / 3.0)
	if sampleSize < 4 {
		sampleSize = 4
	}

	// 计算采样区域
	rect := image.Rect(p.X-sampleSize, p.Y-sampleSize, p.X+sampleSize, p.Y+sampleSize)
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

	// 采样原始颜色区域
	colorRoi := img.Region(rect)
	defer colorRoi.Close()

	// 计算颜色均值和标准差
	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(colorRoi, &meanMat, &stddevMat)

	// 获取 BGR 均值
	bVal := meanMat.GetDoubleAt(0, 0)
	gVal := meanMat.GetDoubleAt(1, 0)
	rVal := meanMat.GetDoubleAt(2, 0)

	// 计算亮度
	brightness := (bVal + gVal + rVal) / 3.0

	// 计算颜色鲜艳度
	maxRGB := math.Max(math.Max(bVal, gVal), rVal)
	minRGB := math.Min(math.Min(bVal, gVal), rVal)
	colorRange := maxRGB - minRGB

	// 1. 过滤背景色：使用更宽松的阈值
	if rVal > 200 && gVal > 150 && bVal > 80 && rVal > gVal+30 && gVal > bVal+30 {
		return ColorNone
	}

	// 2. 过滤颜色鲜艳度：使用更宽松的阈值
	if colorRange > 40 {
		return ColorNone
	}

	// 3. 基于亮度的石头分类：使用更宽松的阈值
	if brightness < 100 {
		// 黑色石头
		return ColorBlack
	} else if brightness > 140 {
		// 白色石头
		return ColorWhite
	}

	return ColorNone
}

// AnalyzeStoneColorStrict 使用更严格的阈值分析棋子颜色，适用于高手数图片
func (d *Detector) AnalyzeStoneColorStrict(img gocv.Mat, p image.Point, r, c int) int {
	// 计算采样半径：网格间距的 1/3 (更集中于棋子中心)
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

	// 使用网格间距的 1/3 作为采样区域
	sampleSize := int(math.Min(float64(hSpace), float64(vSpace)) / 3.0)
	if sampleSize < 4 {
		sampleSize = 4
	}

	// 计算采样区域
	rect := image.Rect(p.X-sampleSize, p.Y-sampleSize, p.X+sampleSize, p.Y+sampleSize)
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

	// 采样原始颜色区域
	colorRoi := img.Region(rect)
	defer colorRoi.Close()

	// 计算颜色均值和标准差
	meanMat := gocv.NewMat()
	defer meanMat.Close()
	stddevMat := gocv.NewMat()
	defer stddevMat.Close()
	gocv.MeanStdDev(colorRoi, &meanMat, &stddevMat)

	// 获取 BGR 均值
	bVal := meanMat.GetDoubleAt(0, 0)
	gVal := meanMat.GetDoubleAt(1, 0)
	rVal := meanMat.GetDoubleAt(2, 0)

	// 计算亮度
	brightness := (bVal + gVal + rVal) / 3.0

	// 计算颜色鲜艳度
	maxRGB := math.Max(math.Max(bVal, gVal), rVal)
	minRGB := math.Min(math.Min(bVal, gVal), rVal)
	colorRange := maxRGB - minRGB

	// 1. 过滤背景色：使用严格的阈值
	if rVal > 190 && gVal > 140 && bVal > 70 && rVal > gVal+25 && gVal > bVal+25 {
		return ColorNone
	}

	// 2. 过滤颜色鲜艳度：使用严格的阈值
	if colorRange > 25 {
		return ColorNone
	}

	// 3. 基于亮度的石头分类：使用严格的阈值
	if brightness < 80 {
		// 黑色石头
		return ColorBlack
	} else if brightness > 160 {
		// 白色石头
		return ColorWhite
	}

	return ColorNone
}
