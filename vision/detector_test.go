package vision

import (
	"testing"
)

func TestBatchRecognition(t *testing.T) {
	imagesDir := "../images"

	// 调用 detector 的批量识别方法
	stats, details, err := BatchRecognizeImages(imagesDir)
	if err != nil {
		t.Fatalf("批量识别失败: %v", err)
	}

	// 打印统计结果
	PrintBatchRecognitionStats(stats, details)

	// 如果需要在测试中验证结果，可以添加断言
	if stats.TotalCount == 0 {
		t.Skip("没有找到测试图像")
	}
}
