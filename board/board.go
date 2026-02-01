package board

import (
	"fmt"
	"image"
)

// Board 表示围棋棋盘的几何模型
type Board struct {
	TopLeft     image.Point // 棋盘左上角(1,1)的像素坐标
	BottomRight image.Point // 棋盘右下角(19,19)的像素坐标
	GridWidth   float64
	GridHeight  float64
}

// NewBoard 创建一个新的棋盘模型
func NewBoard(topLeft, bottomRight image.Point) *Board {
	return &Board{
		TopLeft:     topLeft,
		BottomRight: bottomRight,
		GridWidth:   float64(bottomRight.X-topLeft.X) / 18.0,
		GridHeight:  float64(bottomRight.Y-topLeft.Y) / 18.0,
	}
}

// GetPixelCoordinate 将围棋坐标(0-18, 0-18)转换为屏幕像素坐标
func (b *Board) GetPixelCoordinate(row, col int) image.Point {
	x := float64(b.TopLeft.X) + float64(col)*b.GridWidth
	y := float64(b.TopLeft.Y) + float64(row)*b.GridHeight
	return image.Point{X: int(x), Y: int(y)}
}

// GetGoCoordinate 将屏幕像素坐标转换为围棋坐标(0-18, 0-18)
func (b *Board) GetGoCoordinate(p image.Point) (row, col int) {
	col = int((float64(p.X-b.TopLeft.X) / b.GridWidth) + 0.5)
	row = int((float64(p.Y-b.TopLeft.Y) / b.GridHeight) + 0.5)

	// 边界检查
	if col < 0 {
		col = 0
	}
	if col > 18 {
		col = 18
	}
	if row < 0 {
		row = 0
	}
	if row > 18 {
		row = 18
	}

	return row, col
}

// ConvertToGTP 将数字坐标(0,3)转换为字符串格式
// 适配腾讯围棋：横坐标包含 I (A-S)，纵坐标自上而下 1-19
func ConvertToGTP(row, col int) string {
	letters := "ABCDEFGHIJKLMNOPQRS" // 腾讯围棋包含 I
	colStr := string(letters[col])
	rowStr := row + 1 // 自上而下：0 对应 1，18 对应 19
	return fmt.Sprintf("%s%d", colStr, rowStr)
}
