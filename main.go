package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"time"

	"my-app/vision"

	"github.com/nfnt/resize"
	"gocv.io/x/gocv"
)

const (
	WindowTitle = "my_phone"
	Interval    = 500 * time.Microsecond
	ImageDir    = "/Users/chengjiahua/project/my-app"
	TempImage   = "/Users/chengjiahua/project/my-app/screenshot.jpg"
	TargetW     = 1200
	TargetH     = 2670
)

var detector *vision.Detector

func main() {
	detector = vision.NewDetector()

	fmt.Printf("程序已启动，监控窗口: %s\n", WindowTitle)
	fmt.Println("截图保存路径:", TempImage)
	fmt.Println("按 Ctrl+C 停止程序")
	fmt.Println(strings.Repeat("=", 60))

	go startScrcpy()

	time.Sleep(3 * time.Second)

	for {
		screenshotPath, err := captureWithADB()
		if err != nil {
			fmt.Printf("[%s] 截图失败: %v\n", time.Now().Format("15:04:05"), err)
			time.Sleep(Interval)
			continue
		}

		result, err := recognizeWithVision(screenshotPath)
		if err != nil {
			fmt.Printf("[%s] 识别失败: %v\n", time.Now().Format("15:04:05"), err)
			os.Remove(screenshotPath)
			time.Sleep(Interval)
			continue
		}

		printResult(result)

		os.Remove(screenshotPath)
		time.Sleep(Interval)
	}
}

func startScrcpy() {
	cmd := exec.Command("scrcpy",
		"--window-title", WindowTitle,
		"--always-on-top",
		"--no-control",
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

	fmt.Printf("[%s] 截图成功: %s (%.2f KB)\n",
		time.Now().Format("15:04:05"),
		TempImage,
		float64(getFileSize(TempImage))/1024)

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
	fmt.Printf("[%s] OCR识别结果: moveNumber=%d, err=%v\n", time.Now().Format("15:04:05"), moveNumber, err)

	if err != nil || moveNumber == 0 {
		fmt.Printf("[%s] ⚠️  OCR识别失败或返回0，使用默认策略\n", time.Now().Format("15:04:05"))
	}

	result, err := vision.DetectLastMoveCoord(img, moveNumber)
	if err != nil {
		return &result, nil
	}

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

	if len(r.Debug) > 0 {
		if status, ok := r.Debug["final_status"].(string); ok {
			fmt.Printf("    └── 状态: %s\n", status)
		}
	}
}
