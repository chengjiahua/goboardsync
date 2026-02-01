package controller

import (
	"fmt"
	"my-app/board"
	"time"

	"github.com/go-vgo/robotgo"
)

type SyncController struct {
	KaTrainTitle string
}

func NewSyncController() *SyncController {
	return &SyncController{
		KaTrainTitle: "KaTrain",
	}
}

func (s *SyncController) SyncMove(row, col int) error {
	gtpCoord := board.ConvertToGTP(row, col)
	fmt.Printf("准备同步到 KaTrain: %s\n", gtpCoord)

	err := robotgo.ActiveName(s.KaTrainTitle)
	if err != nil {
		return fmt.Errorf("无法激活 KaTrain 窗口: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	robotgo.TypeStr(gtpCoord)
	robotgo.KeyTap("enter")

	fmt.Printf("成功发送指令: %s + Enter\n", gtpCoord)
	return nil
}
