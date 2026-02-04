package main

import (
	"fmt"
	"image"
	"my-app/board"
	"my-app/controller"
	"my-app/vision"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-vgo/robotgo"
	"github.com/olahol/melody"
	"gocv.io/x/gocv"
)

var (
	boardModel *board.Board
	detector   *vision.Detector
	syncCtrl   *controller.SyncController
	m          *melody.Melody
)

func init() {
	boardModel = board.NewBoard(image.Point{X: 100, Y: 100}, image.Point{X: 600, Y: 600})
	detector = vision.NewDetector(boardModel)
	syncCtrl = controller.NewSyncController()
}

func main() {
	r := gin.Default()
	m = melody.New()

	r.GET("/ws", func(c *gin.Context) {
		m.HandleRequest(c.Writer, c.Request)
	})

	r.StaticFile("/", "./static/index.html")

	r.POST("/calibrate", func(c *gin.Context) {
		var config struct {
			TopLeft     image.Point `json:"top_left"`
			BottomRight image.Point `json:"bottom_right"`
		}
		if err := c.BindJSON(&config); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		boardModel = board.NewBoard(config.TopLeft, config.BottomRight)
		detector = vision.NewDetector(boardModel)
		c.JSON(http.StatusOK, gin.H{"status": "calibrated"})
	})

	go monitorLoop()

	fmt.Println("服务器已启动: http://localhost:8080")
	r.Run(":8080")
}

func monitorLoop() {
	for {
		// 在 macOS 上，我们可以通过标题直接查找
		fids, _ := robotgo.FindIds("Tencent Weiqi")
		if len(fids) == 0 {
			fids, _ = robotgo.FindIds("腾讯围棋")
		}

		if len(fids) > 0 {
			targetID := fids[0]
			x, y, w, h := robotgo.GetBounds(targetID)
			img, err := robotgo.CaptureImg(x, y, w, h)
			if err == nil {
				mat, err := gocv.ImageToMatRGB(img)
				if err == nil {
					row, col, _, _ := detector.DetectLatestMove(mat)
					if row != -1 && col != -1 {
						fmt.Printf("检测到新落子: [%d, %d]\n", row, col)
						syncCtrl.SyncMove(row, col)
						msg := fmt.Sprintf("move:%d,%d", row, col)
						m.Broadcast([]byte(msg))
					}
					mat.Close()
				}
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}
