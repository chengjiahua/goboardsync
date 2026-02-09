package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"my-app/vision"

	"github.com/nfnt/resize"
	"gocv.io/x/gocv"
)

const (
	WindowTitle   = "my_phone"
	Interval      = 100 * time.Microsecond
	ImageDir      = "/Users/chengjiahua/project/my-app"
	TempImage     = "/Users/chengjiahua/project/my-app/screenshot.jpg"
	TargetW       = 1200
	TargetH       = 2670
	POLL_INTERVAL = 1000 * time.Microsecond
)

var (
	detector        *vision.Detector
	KATRAIN_URL     = "http://localhost:8080"
	lastKatrainMove int
	lastPhoneMove   int
	mu              sync.RWMutex
)

func main() {
	detector = vision.NewDetector()

	fmt.Printf("🚀 程序已启动\n")
	fmt.Printf("   监控窗口: %s\n", WindowTitle)
	fmt.Printf("   截图保存路径: %s\n", TempImage)
	fmt.Printf("   KaTrain API: %s\n", KATRAIN_URL)
	fmt.Printf("   屏幕分辨率: %dx%d\n", TargetW, TargetH)
	fmt.Println("   按 Ctrl+C 停止程序")
	fmt.Println(strings.Repeat("=", 60))

	go startScrcpy()

	time.Sleep(1 * time.Second)

	fmt.Printf("[%s] 🔄 启动双向同步...\n", time.Now().Format("15:04:05"))
	fmt.Printf("[%s] 📱 监听手机 → KaTrain\n", time.Now().Format("15:04:05"))
	fmt.Printf("[%s] 🖥️  监听 KaTrain → 手机\n", time.Now().Format("15:04:05"))
	fmt.Println(strings.Repeat("=", 60))

	go syncPhoneToKatrain()
	go syncKatrainToPhone()

	select {}
}

func startScrcpy() {
	cmd := exec.Command("scrcpy",
		"--window-title", WindowTitle,
		"--always-on-top",
		"--max-fps", "15",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func captureWithADB() (string, error) {
	adbPath, err := exec.LookPath("adb")
	if err != nil {
		return "", fmt.Errorf("未找到 adb: %v", err)
	}

	timestamp := time.Now().UnixNano()
	remotePath := fmt.Sprintf("/sdcard/go_screenshot_%d.png", timestamp)
	tempPNGPath := fmt.Sprintf("/Users/chengjiahua/project/my-app/temp_%d.png", timestamp)

	capCmd := exec.Command(adbPath, "shell", "screencap", "-p", remotePath)
	if err := capCmd.Run(); err != nil {
		return "", fmt.Errorf("ADB 截图失败: %v", err)
	}

	pullCmd := exec.Command("adb", "pull", remotePath, tempPNGPath)
	if err := pullCmd.Run(); err != nil {
		return "", fmt.Errorf("拉取截图失败: %v", err)
	}

	rmCmd := exec.Command("adb", "shell", "rm", remotePath)
	rmCmd.Run()

	if _, err := os.Stat(tempPNGPath); os.IsNotExist(err) {
		return "", fmt.Errorf("截图文件未生成")
	}

	err = convertPNGtoJPG(tempPNGPath, TempImage)
	os.Remove(tempPNGPath)
	if err != nil {
		return "", fmt.Errorf("转换格式失败: %v", err)
	}

	return TempImage, nil
}

func convertPNGtoJPG(pngPath, jpgPath string) error {
	file, err := os.Open(pngPath)
	if err != nil {
		return err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return err
	}

	out, err := os.Create(jpgPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return jpeg.Encode(out, img, &jpeg.Options{Quality: 90})
}

func getFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func resizeImage(imagePath string, targetW, targetH int) error {
	file, err := os.Open(imagePath)
	if err != nil {
		return err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return err
	}

	newImg := resize.Resize(uint(targetW), uint(targetH), img, resize.Lanczos3)

	out, err := os.Create(imagePath)
	if err != nil {
		return err
	}
	defer out.Close()

	return png.Encode(out, newImg)
}

func recognizeWithVision(imagePath string) (*vision.Result, error) {
	err := resizeImage(imagePath, TargetW, TargetH)
	if err != nil {
		fmt.Printf("[%s] 图片缩放失败: %v\n", time.Now().Format("15:04:05"), err)
	}

	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		return nil, fmt.Errorf("无法读取图片")
	}
	defer img.Close()

	moveNumber, err := detector.FetchMoveNumberFromOCR(img)
	// fmt.Printf("[%s] OCR识别结果: moveNumber=%d, err=%v\n", time.Now().Format("15:04:05"), moveNumber, err)

	if err != nil || moveNumber == 0 {
		fmt.Printf("[%s] ⚠️  OCR识别失败或返回0，使用默认策略\n", time.Now().Format("15:04:05"))
	}

	result, err := vision.DetectLastMoveCoord(img, moveNumber)
	if err != nil {
		return &result, nil
	}
	printResult(&result)
	return &result, nil
}

func printResult(r *vision.Result) {
	colorName := "黑棋"
	if r.Color == "W" {
		colorName = "白棋"
	}

	xLetter := string(rune('A' + r.X - 1))
	if xLetter > "S" {
		xLetter = "T"
	}

	fmt.Printf("[%s] ✅ 第 %d 手 - %s - 坐标: %s%d\n",
		time.Now().Format("15:04:05"),
		r.Move,
		colorName,
		xLetter,
		r.Y,
	)

}

func checkPosition(x, y int) (bool, string, error) {
	url := fmt.Sprintf("%s/api/check-position?x=%d&y=%d", KATRAIN_URL, x, y)
	resp, err := http.Get(url)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success  bool   `json:"success"`
		HasStone bool   `json:"has_stone"`
		Player   string `json:"player"`
		Error    string `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return false, "", err
	}

	if !result.Success {
		return false, "", fmt.Errorf("API错误: %s", result.Error)
	}

	return result.HasStone, result.Player, nil
}

func makeMove(x, y int, player string) error {
	url := fmt.Sprintf("%s/api/make-move", KATRAIN_URL)

	data := fmt.Sprintf(`{"x": %d, "y": %d, "player": "%s"}`, x, y, player)
	fmt.Printf("[%s] 发送请求: %s\n", time.Now().Format("15:04:05"), data)

	resp, err := http.Post(url, "application/json", strings.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("解析响应失败: %s", string(body))
	}

	if !result.Success {
		return fmt.Errorf("落子失败: %s", result.Error)
	}

	return nil
}

func getLastMove() (int, int, string, int, error) {
	url := fmt.Sprintf("%s/api/last-move", KATRAIN_URL)
	resp, err := http.Get(url)
	if err != nil {
		return 0, 0, "", 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success    bool   `json:"success"`
		MoveNumber int    `json:"move_number"`
		Error      string `json:"error"`
		LastMove   struct {
			Player     string `json:"player"`
			MoveNumber int    `json:"move_number"`
			Coords     []int  `json:"coords"`
		} `json:"last_move"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, "", 0, err
	}

	if !result.Success {
		return 0, 0, "", 0, fmt.Errorf("API错误: %s", result.Error)
	}

	if result.LastMove.Coords == nil {
		return 0, 0, "", 0, nil
	}

	return result.LastMove.Coords[0], result.LastMove.Coords[1], result.LastMove.Player, result.LastMove.MoveNumber, nil
}

// func gridToScreen(gridX, gridY int) (int, int) {
// 	boardLeft := 40
// 	boardTop := 536
// 	boardRight := 1160
// 	boardBottom := 1650

// 	boardWidth := boardRight - boardLeft
// 	boardHeight := boardBottom - boardTop

// 	cellW := float64(boardWidth) / 18.0
// 	cellH := float64(boardHeight) / 18.0

// 	screenX := boardLeft + int(float64(gridX)*cellW+cellW/2)
// 	screenY := boardTop + int(float64(gridY)*cellH+cellH/2)

// 	return screenX, screenY
// }

func gridToScreen(x, y int) (int, int) {
	// 针对 1200x2670 分辨率的腾讯围棋 App 精确对齐
	// x: KaTrain 的 X 坐标 (0-18)，0代表A线，18代表S线
	// y: KaTrain 的 Y 坐标 (0-18)，0代表底部(19线)，18代表顶部(1线)

	const (
		// A线 (第1根纵线) 的中心 X 像素
		startX = 60.0
		// 1线 (第1根横线) 的中心 Y 像素
		startY = 560.0
		// 棋盘格子的精确间距 (像素)
		gap = 60.0
	)

	// 计算 X 轴：从左向右增加
	// 公式：起始点 + 索引 * 间距
	screenX := startX + float64(x)*gap

	// 计算 Y 轴：KaTrain 的 Y=0 是最下面，而屏幕坐标 Y 是从上往下算的
	// 所以需要翻转：屏幕Y = 起始点 + (18 - KaTrainY) * 间距
	screenY := startY + float64(18-y)*gap

	// 打印一下，方便你在日志里核对
	// fmt.Printf("[坐标转换] KaTrain(%d,%d) -> 屏幕(%d,%d)\n", x, y, int(screenX), int(screenY))

	return int(screenX), int(screenY)
}

func tapOnPhone(gridX, gridY int) error {
	fmt.Printf("[%s] 🎯 准备落子: gridX:%d, gridY:%d\n", time.Now().Format("15:04:05"), gridX, gridY)

	// 1. 计算棋盘落子点的屏幕坐标
	screenX, screenY := gridToScreen(gridX, gridY)

	adbPath, err := exec.LookPath("adb")
	if err != nil {
		return fmt.Errorf("未找到 adb: %v", err)
	}

	// 2. 执行第一次点击：移动落子指示标
	cmd1 := exec.Command(adbPath, "shell", "input", "tap", fmt.Sprintf("%d", screenX), fmt.Sprintf("%d", screenY))
	if err := cmd1.Run(); err != nil {
		return fmt.Errorf("移动指示标失败: %v", err)
	}
	fmt.Printf("[%s] 📍 已移动指针到: (%d, %d)\n", time.Now().Format("15:04:05"), screenX, screenY)

	// 3. 等待 300 毫秒，确保 App 反应过来了
	time.Sleep(300 * time.Millisecond)

	// 4. 执行第二次点击：点击“确认”按钮 (坐标 600, 2150)
	confirmX, confirmY := 600, 2150
	cmd2 := exec.Command(adbPath, "shell", "input", "tap", fmt.Sprintf("%d", confirmX), fmt.Sprintf("%d", confirmY))
	if err := cmd2.Run(); err != nil {
		return fmt.Errorf("点击确认按钮失败: %v", err)
	}

	// 打印输出
	xLetter := string(rune('A' + gridX)) // 修正字母显示逻辑
	if xLetter >= "I" {
		xLetter = string(rune('A' + gridX + 1))
	} // 跳过 'I' 是某些棋盘的习惯，腾讯围棋通常不跳过

	fmt.Printf("[%s] ✅ 落子成功！已点击“确认”按钮 (屏幕坐标: %d, %d)\n",
		time.Now().Format("15:04:05"),
		confirmX,
		confirmY,
	)

	return nil
}
func syncPhoneToKatrain() {
	for {
		screenshotPath, err := captureWithADB()
		if err != nil {
			fmt.Printf("[%s] 📸 截图失败: %v\n", time.Now().Format("15:04:05"), err)
			time.Sleep(Interval)
			continue
		}

		fmt.Printf("[%s] 📸 截图成功: %s\n", time.Now().Format("15:04:05"), screenshotPath)

		result, err := recognizeWithVision(screenshotPath)
		if err != nil {
			fmt.Printf("[%s] ❌ 识别失败: %v\n", time.Now().Format("15:04:05"), err)
			os.Remove(screenshotPath)
			time.Sleep(Interval)
			continue
		}

		fmt.Printf("[%s] ✅ 识别成功: 第 %d 手, 坐标: %d-%d, 颜色: %s\n",
			time.Now().Format("15:04:05"),
			result.Move,
			result.X,
			result.Y,
			result.Color,
		)

		mu.Lock()
		isNewFromPhone := result.Move > lastPhoneMove
		mu.Unlock()

		if isNewFromPhone {
			fmt.Printf("[%s] 🔄 检测到新手: %d > %d  X:%d  Y:%d\n", time.Now().Format("15:04:05"), result.Move, lastPhoneMove, result.X, result.Y)
			colorForKatrain := result.Color
			katrainX, katrainY := phoneGridToKatrain(result.X, result.Y)
			hasStone, _, err := checkPosition(katrainX, katrainY)
			if err != nil {
				fmt.Printf("[%s] ❌ 检查位置失败: %v\n", time.Now().Format("15:04:05"), err)
			} else if !hasStone {
				err := makeMove(katrainX, katrainY, colorForKatrain)
				if err != nil {
					fmt.Printf("[%s] ❌ 同步落子失败: %v\n", time.Now().Format("15:04:05"), err)
				} else {
					fmt.Printf("[%s] ✅ 手机→KaTrain: 第 %d 手 %s %s%d\n",
						time.Now().Format("15:04:05"),
						result.Move,
						mapColorToChinese(colorForKatrain),
						string(rune('A'+katrainX)),
						katrainY+1,
					)
				}
			} else {
				fmt.Printf("[%s] ℹ️  KaTrain 已有棋子，跳过: %s%d\n",
					time.Now().Format("15:04:05"),
					string(rune('A'+katrainX)),
				)
			}

			mu.Lock()
			lastPhoneMove = result.Move
			mu.Unlock()
		}

		os.Remove(screenshotPath)
		time.Sleep(Interval)
	}
}

func phoneGridToKatrain(x, y int) (katrainX int, katrainY int) {
	katrainX = x - 1
	katrainY = 19 - y
	return
}
func syncKatrainToPhone() {
	for {
		x, y, _, moveNumber, err := getLastMove()
		fmt.Printf("[%s] ✅ 获取 KaTrain 最后一手: X:%d Y:%d (手数: %d)\n",
			time.Now().Format("15:04:05"),
			x,
			y,
			moveNumber,
		)
		if err != nil {
			fmt.Printf("[%s] ❌ 获取 KaTrain 最后一手失败: %v\n", time.Now().Format("15:04:05"), err)
			time.Sleep(POLL_INTERVAL)
			continue
		}

		if x == 0 && y == 0 {
			time.Sleep(POLL_INTERVAL)
			continue
		}

		mu.Lock()
		isNewFromKatrain := moveNumber > lastKatrainMove
		mu.Unlock()

		if isNewFromKatrain {
			err := tapOnPhone(x, y)
			if err != nil {
				fmt.Printf("[%s] ❌ 手机点击失败: %v\n", time.Now().Format("15:04:05"), err)
			}

			mu.Lock()
			lastKatrainMove = moveNumber
			mu.Unlock()
		}

		time.Sleep(POLL_INTERVAL)
	}
}

func mapColorToChinese(color string) string {
	if color == "B" {
		return "黑棋"
	}
	return "白棋"
}
