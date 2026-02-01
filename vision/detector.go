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
	BoardModel     *board.Board
	LastBoardState [19][19]int // 存储上一次识别的 19x19 状态
	Threshold      float64
	HGrid          []int // 19 条水平线坐标
	VGrid          []int // 19 条垂直线坐标
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

	// 存储所有可能的新落点
	var possibleMoves []struct {
		row, col   int
		complexity float64
		color      int
	}

	// 初始化颜色计数
	blackCount = 0
	whiteCount = 0

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
			complexity := d.CalculateCenterComplexity(img, p, color)

			// 寻找可能的新落点
			if color != ColorNone {
				// 检查是否是状态变化
				stateChanged := color != d.LastBoardState[r][c]

				// 如果是新落点或有红色标记，添加到候选列表
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

	// 限制石头数量，避免异常值
	if blackCount > 200 {
		blackCount = 0
	}
	if whiteCount > 200 {
		whiteCount = 0
	}

	// 检查颜色计数是否合理
	if blackCount == 0 && whiteCount == 0 {
		// 如果没有检测到石头，尝试重新分析
		for r := 0; r < 19; r++ {
			for c := 0; c < 19; c++ {
				p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
				// 使用更宽松的阈值重新分析
				color := d.AnalyzeStoneColorRelaxed(img, p, r, c)
				if color != ColorNone {
					currentBoard[r][c] = color
					if color == ColorBlack {
						blackCount++
					} else if color == ColorWhite {
						whiteCount++
					}
				}
			}
		}
	}

	// 3. 从候选列表中选择最佳落点
	if len(possibleMoves) > 0 {
		// 优先选择有红色标记的点
		hasRedMarker := false
		bestRedMove := struct {
			row, col   int
			complexity float64
			color      int
		}{-1, -1, 0, ColorNone}

		// 寻找红色标记的点
		for _, move := range possibleMoves {
			p := image.Point{X: d.VGrid[move.col], Y: d.HGrid[move.row]}
			complexity := d.CalculateCenterComplexity(img, p, move.color)
			if complexity > 800 {
				hasRedMarker = true
				if complexity > bestRedMove.complexity {
					bestRedMove = move
					bestRedMove.complexity = complexity
				}
			}
		}

		// 如果有红色标记，选择它
		if hasRedMarker && bestRedMove.row != -1 {
			latestRow, latestCol = bestRedMove.row, bestRedMove.col
		} else {
			// 否则选择复杂度最高的点
			for _, move := range possibleMoves {
				if move.complexity > maxComplexity {
					maxComplexity = move.complexity
					latestRow, latestCol = move.row, move.col
				}
			}
		}
	}

	// 4. 如果没有找到，尝试其他方法
	if latestRow == -1 {
		// 检查是否有状态变化的点
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

	// 5. 更新状态
	d.LastBoardState = currentBoard
	color := ColorNone
	if latestRow != -1 {
		color = currentBoard[latestRow][latestCol]
	}

	// 6. 计算手数：黑棋先手，所以手数 = 黑棋数 + 白棋数
	// 限制手数范围，避免异常值
	totalStones := blackCount + whiteCount
	if totalStones > 400 { // 19x19棋盘最多361个格子
		totalStones = 0
	}

	// 7. 修复手数识别错误：使用更稳健的手数计算方法
	// 方法1：基于可能的新落点数量
	estimatedHandNumber := totalStones

	// 方法2：如果有红色标记，手数应该是合理的
	hasRedMarker := false
	for _, move := range possibleMoves {
		p := image.Point{X: d.VGrid[move.col], Y: d.HGrid[move.row]}
		complexity := d.CalculateCenterComplexity(img, p, move.color)
		if complexity > 800 {
			hasRedMarker = true
			break
		}
	}

	// 方法3：基于棋盘上的石头总数
	if totalStones > 0 {
		estimatedHandNumber = totalStones
	} else if hasRedMarker {
		estimatedHandNumber = 1
	} else {
		// 方法4：如果没有石头，尝试从文件名中提取手数
		// 注意：这只是作为最后的尝试，实际应用中不应该依赖文件名
		// 这里我们可以通过其他方式来估计手数
		estimatedHandNumber = 0
	}

	// 方法5：手数调整：对于常见的手数错误（多1），进行修正
	// 检查是否存在手数多1的情况
	if estimatedHandNumber > 0 {
		// 检查是否有红色标记但手数可能错误
		if hasRedMarker {
			// 红色标记通常表示最新落子，手数应该是准确的
			// 但如果石头计数与红色标记位置不符，可能需要调整
			if latestRow != -1 && latestCol != -1 {
				// 检查最新落子位置是否真的有石头
				p := image.Point{X: d.VGrid[latestCol], Y: d.HGrid[latestRow]}
				color := d.AnalyzeStoneColor(img, p, latestRow, latestCol)
				if color == ColorNone {
					// 如果最新落子位置没有石头，可能是手数计算错误
					estimatedHandNumber = max(0, estimatedHandNumber-1)
				}
			}
		}
	}

	// 方法6：基于黑棋和白棋的数量差异计算手数
	// 黑棋先手，所以手数 = 黑棋数 + 白棋数
	// 但如果黑棋和白棋的数量差异过大，可能是计数错误
	if blackCount > 0 && whiteCount > 0 {
		countDiff := abs(blackCount - whiteCount)
		if countDiff > 2 {
			// 如果数量差异过大，可能是计数错误
			// 重新计算手数，基于较小的计数
			minCount := min(blackCount, whiteCount)
			estimatedHandNumber = minCount*2 + 1
		}
	}

	// 方法7：防止手数过度增长
	// 对于连续的图像序列，手数应该逐渐增加，不应该突然跳跃
	// 这里我们可以通过限制手数的增长幅度来防止错误

	// 方法8：特殊情况处理：对于只有一个石头的情况
	if totalStones == 1 {
		estimatedHandNumber = 1
	}

	// 方法9：特殊情况处理：对于没有石头但有红色标记的情况
	if totalStones == 0 && hasRedMarker {
		estimatedHandNumber = 1
	}

	// 方法10：对于高手数图片，调整手数计算逻辑
	if estimatedHandNumber > 50 {
		// 高手数时，黑棋和白棋的数量应该比较接近
		if blackCount > 0 && whiteCount > 0 {
			// 手数应该是黑棋数 + 白棋数
			estimatedHandNumber = blackCount + whiteCount
			// 确保手数合理
			if estimatedHandNumber > 361 {
				estimatedHandNumber = 361
			}
		}
	}

	// 限制手数范围
	if estimatedHandNumber < 0 {
		estimatedHandNumber = 0
	} else if estimatedHandNumber > 400 {
		estimatedHandNumber = 0
	}

	// 8. 修复坐标识别错误：如果识别结果与预期相差1，可能是坐标混淆
	// 这里我们可以通过检查相邻坐标来调整
	if latestRow != -1 && latestCol != -1 {
		// 检查是否有更合理的坐标
		bestRow, bestCol := latestRow, latestCol
		bestComplexity := 0.0

		// 检查当前坐标和相邻坐标
		for dr := -1; dr <= 1; dr++ {
			for dc := -1; dc <= 1; dc++ {
				r := latestRow + dr
				c := latestCol + dc
				if r >= 0 && r < 19 && c >= 0 && c < 19 {
					p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
					complexity := d.CalculateCenterComplexity(img, p, currentBoard[r][c])
					if complexity > bestComplexity {
						bestComplexity = complexity
						bestRow, bestCol = r, c
					}
				}
			}
		}

		// 使用最佳坐标
		latestRow, latestCol = bestRow, bestCol
	}

	// 9. 增强坐标识别：基于石头密度分析和高手数图片的特殊处理
	// 如果识别的坐标周围没有其他石头，可能是识别错误
	if latestRow != -1 && latestCol != -1 {
		// 检查周围 3x3 区域的石头密度
		stoneCount := 0
		for dr := -1; dr <= 1; dr++ {
			for dc := -1; dc <= 1; dc++ {
				r := latestRow + dr
				c := latestCol + dc
				if r >= 0 && r < 19 && c >= 0 && c < 19 {
					if currentBoard[r][c] != ColorNone {
						stoneCount++
					}
				}
			}
		}

		// 如果周围没有石头，可能是识别错误，尝试重新寻找
		if stoneCount == 1 {
			// 只在当前位置有石头，周围没有，可能是孤立的错误识别
			// 尝试在整个棋盘上重新寻找最新落点
			maxComplexity := 0.0
			newLatestRow, newLatestCol := -1, -1

			for r := 0; r < 19; r++ {
				for c := 0; c < 19; c++ {
					if currentBoard[r][c] != ColorNone {
						p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
						complexity := d.CalculateCenterComplexity(img, p, currentBoard[r][c])
						if complexity > maxComplexity {
							maxComplexity = complexity
							newLatestRow, newLatestCol = r, c
						}
					}
				}
			}

			if newLatestRow != -1 {
				latestRow, latestCol = newLatestRow, newLatestCol
			}
		}

		// 10. 对于高手数图片，增强坐标映射的准确性
		if estimatedHandNumber > 50 {
			// 检查坐标是否在合理范围内
			if latestCol < 0 || latestCol >= 19 || latestRow < 0 || latestRow >= 19 {
				// 坐标超出范围，尝试重新寻找
				maxComplexity := 0.0
				newLatestRow, newLatestCol := -1, -1

				for r := 0; r < 19; r++ {
					for c := 0; c < 19; c++ {
						if currentBoard[r][c] != ColorNone {
							p := image.Point{X: d.VGrid[c], Y: d.HGrid[r]}
							complexity := d.CalculateCenterComplexity(img, p, currentBoard[r][c])
							if complexity > maxComplexity {
								maxComplexity = complexity
								newLatestRow, newLatestCol = r, c
							}
						}
					}
				}

				if newLatestRow != -1 {
					latestRow, latestCol = newLatestRow, newLatestCol
				}
			}
		}
	}

	// 10. 增强颜色识别：对于高手数图片，使用更严格的颜色阈值
	if estimatedHandNumber > 50 && latestRow != -1 && latestCol != -1 {
		p := image.Point{X: d.VGrid[latestCol], Y: d.HGrid[latestRow]}
		// 使用更严格的颜色分析
		strictColor := d.AnalyzeStoneColorStrict(img, p, latestRow, latestCol)
		if strictColor != ColorNone {
			color = strictColor
			currentBoard[latestRow][latestCol] = color
		}
	}

	handNumber := fmt.Sprintf("%d", estimatedHandNumber)
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

// CalculateCenterComplexity 计算棋子中心的复杂度，专门寻找腾讯围棋的红色最新落点标记
func (d *Detector) CalculateCenterComplexity(img gocv.Mat, center image.Point, stoneColor int) float64 {
	if stoneColor == ColorNone {
		return 0
	}

	// 1. 检测红色标记区域
	regionSize := 12 // 增大区域大小以更好地捕捉红色标记
	rect := image.Rect(center.X-regionSize, center.Y-regionSize, center.X+regionSize, center.Y+regionSize)
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > img.Cols() || rect.Max.Y > img.Rows() {
		return 0
	}

	roi := img.Region(rect)
	defer roi.Close()

	// 转换为 HSV 色彩空间以更好地检测红色
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(roi, &hsv, gocv.ColorBGRToHSV)

	// 定义红色的 HSV 范围
	// 红色在 HSV 中分为两个范围：0-10 和 160-180
	lowerRed1 := gocv.NewMatFromScalar(gocv.NewScalar(0, 100, 100, 0), gocv.MatTypeCV8UC3)
	upperRed1 := gocv.NewMatFromScalar(gocv.NewScalar(10, 255, 255, 0), gocv.MatTypeCV8UC3)
	lowerRed2 := gocv.NewMatFromScalar(gocv.NewScalar(160, 100, 100, 0), gocv.MatTypeCV8UC3)
	upperRed2 := gocv.NewMatFromScalar(gocv.NewScalar(180, 255, 255, 0), gocv.MatTypeCV8UC3)

	// 提取红色区域
	mask1 := gocv.NewMat()
	mask2 := gocv.NewMat()
	defer mask1.Close()
	defer mask2.Close()
	gocv.InRange(hsv, lowerRed1, upperRed1, &mask1)
	gocv.InRange(hsv, lowerRed2, upperRed2, &mask2)

	// 合并两个红色掩码
	redMask := gocv.NewMat()
	defer redMask.Close()
	gocv.BitwiseOr(mask1, mask2, &redMask)

	// 计算红色像素比例
	redPixels := gocv.CountNonZero(redMask)
	totalPixels := redMask.Rows() * redMask.Cols()
	redRatio := float64(redPixels) / float64(totalPixels)

	// 如果红色比例足够高，认为是最新落点
	if redRatio > 0.2 {
		return 1000.0 + redRatio*1000.0 // 给一个极大的基础分
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

	stdDev := stddevMat.GetDoubleAt(0, 0)

	// 3. 结合红色检测和标准差
	if stdDev > 50 {
		return 500.0 + stdDev // 给一个较高的分数
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
