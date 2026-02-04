# Vision 模块调试文档

## 目录结构

```
vision/
├── detector.go              # 主检测器代码
├── detector_test.go         # 测试文件
├── debug/                   # 调试输出目录
│   ├── {手数}-{坐标}-{颜色}/
│   │   ├── debug_boardWarp.jpg          # 棋盘透视矫正后的图像
│   │   ├── debug_source_corners.jpg     # 原图上标注棋盘四角
│   │   ├── debug_mark_mask.jpg          # 角标颜色分割的mask图像
│   │   ├── debug_overlay.jpg            # 叠加标记的调试图
│   │   ├── debug_corners.json           # 棋盘角点、角标位置、网格映射信息
│   │   └── black_mark_debug.json        # 黑棋红色角标识别详细参数（仅黑棋）
│   └── ...
└── README.md                # 本文档
```

## Debug 文件说明

### 1. debug_boardWarp.jpg
**描述**: 棋盘透视矫正后的图像

**用途**: 
- 展示棋盘经过透视变换后的正视图
- 用于验证棋盘四角定位是否准确
- 后续所有角标检测和网格计算都基于此图像

**生成时机**: 在 `SaveDebugImages` 函数中，使用 `WarpBoard` 函数对原图进行透视变换后生成

---

### 2. debug_source_corners.jpg
**描述**: 原图上标注棋盘四角的图像

**用途**:
- 可视化验证硬编码的棋盘四角位置是否正确
- 红色圆点标记四个角点，红色连线标记棋盘边界
- 用于调试和调整 `FixedBoardCorners` 配置

**生成时机**: 在 `SaveDebugImages` 函数中，在原图上绘制角点和连线后生成

---

### 3. debug_mark_mask.jpg
**描述**: 角标颜色分割的mask图像

**用途**:
- 展示角标颜色分割的结果
- 白色区域表示检测到的角标颜色区域
- 黑色区域表示非角标区域
- 用于调试颜色阈值设置

**颜色说明**:
- **黑棋**: 红色角标，使用HSV颜色空间分割（H: 0-15 和 160-180, S: 100-255, V: 100-255）
- **白棋**: 蓝色角标，使用HSV颜色空间分割（H: 90-135, S: 80-255, V: 80-255）

**生成时机**: 在 `saveMarkMask` 函数中，使用HSV颜色分割后生成

---

### 4. debug_overlay.jpg
**描述**: 叠加标记的调试图

**用途**:
- 综合展示识别结果的可视化图像
- 绿色矩形: 棋盘内矩形区域
- 红色圆点: 检测到的角标中心点
- 红色矩形: 角标边界框
- 黄色小点: 所有19×19网格交叉点

**生成时机**: 在 `saveOverlayImage` 函数中，在矫正后的图像上绘制各种标记后生成

---

### 5. debug_corners.json
**描述**: 棋盘角点、角标位置、网格映射信息

**用途**:
- 记录识别过程中的关键参数
- 用于验证识别结果的正确性
- 便于离线分析和调试

**字段说明**:
```json
{
  "resKey": "1200x2670",           // 图像分辨率标识
  "imgWidth": 1200,                // 图像宽度
  "imgHeight": 2670,               // 图像高度
  "corners": [                     // 棋盘四角坐标
    {"X": 40, "Y": 536},
    {"X": 1160, "Y": 536},
    {"X": 1160, "Y": 1650},
    {"X": 40, "Y": 1650}
  ],
  "mark_point": {                  // 检测到的角标中心点
    "X": 511,
    "Y": 855
  },
  "mark_cell": {                   // 角标所在的网格单元
    "Min": {"X": 483, "Y": 827},
    "Max": {"X": 539, "Y": 883}
  },
  "mark_rb": {                     // 角标单元右下角点
    "X": 539,
    "Y": 883
  },
  "mapped_grid": {                 // 映射到的网格坐标
    "col": 9,
    "row": 16
  },
  "mapping_confidence": 0          // 映射置信度
}
```

**生成时机**: 在 `SaveDebugImages` 函数中，计算完网格映射后生成

---

### 6. black_mark_debug.json (仅黑棋)
**描述**: 黑棋红色角标识别的详细参数

**用途**:
- 详细记录黑棋红色角标识别过程中的所有核心参数
- 用于调试和优化红色角标检测算法
- 帮助理解识别失败的原因

**字段说明**:
```json
{
  "move_number": 1,                // 手数
  "image_size": "1024x1024",       // 图像尺寸
  "detection_method": "HSV_BLACK", // 检测方法
  "success": true,                 // 是否成功识别
  
  "hsv_color_range": {             // HSV颜色范围
    "lower_red1": {"h": 0.0, "s": 100.0, "v": 100.0},
    "upper_red1": {"h": 15.0, "s": 255.0, "v": 255.0},
    "lower_red2": {"h": 160.0, "s": 100.0, "v": 100.0},
    "upper_red2": {"h": 180.0, "s": 255.0, "v": 255.0}
  },
  
  "gaussian_blur": {               // 高斯模糊参数
    "kernel_size": {"x": 3, "y": 3},
    "sigma_x": 1.0,
    "sigma_y": 1.0
  },
  
  "morphology": {                  // 形态学操作参数
    "kernel_size": {"x": 3, "y": 3},
    "kernel_type": "Ellipse",
    "operation": "Close"
  },
  
  "contour_info": {                // 轮廓信息
    "total_contours": 5,           // 检测到的总轮廓数
    "valid_contours": 2            // 通过筛选的有效轮廓数
  },
  
  "selected_contour": {            // 选中的最佳轮廓
    "index": 0,                     // 轮廓索引
    "area": 125.5,                  // 轮廓面积
    "perimeter": 45.2,              // 轮廓周长
    "circularity": 0.77,            // 圆形度 (4πA/P²)
    "aspect_ratio": 1.2,            // 宽高比
    "score": 450.3,                 // 综合得分
    "bounds": {                     // 外接矩形
      "min": {"x": 495, "y": 840},
      "max": {"x": 525, "y": 870}
    }
  },
  
  "final_mark_point": {            // 最终角标中心点
    "x": 510,
    "y": 855
  },
  
  "error_message": ""               // 错误信息（失败时）
}
```

**核心参数说明**:

1. **HSV颜色范围**: 红色在HSV颜色空间中分布在两个区间（0-15和160-180），需要同时检测

2. **高斯模糊**: 用于平滑mask，减少噪点影响

3. **形态学操作**: 使用闭操作（先膨胀后腐蚀）填充小洞，使轮廓更完整

4. **轮廓筛选标准**:
   - 面积: 4 < area < 5000
   - 宽高比: ratio < 5.0
   - 综合得分: score = area × (6.0 - ratio) × (circularity + 0.5)

5. **圆形度**: 衡量轮廓接近圆形的程度，圆形轮廓的圆形度接近1，三角形轮廓的圆形度较低

**生成时机**: 在 `SaveBlackMarkDebugInfo` 函数中，仅在黑棋（奇数手数）时生成

---

## 使用方法

### 生成调试文件

运行批量识别测试会自动生成调试文件：

```bash
go test -v ./vision
```

或手动调用：

```go
import "my-app/vision"

img := gocv.IMRead("path/to/image.jpg", gocv.IMReadColor)
moveNumber := 1
debugDir := "debug/1-P4-black"
vision.SaveDebugImages(img, moveNumber, debugDir)
```

### 查看调试信息

1. **可视化检查**: 查看 `.jpg` 图像文件，直观了解识别过程
2. **参数分析**: 查看 `.json` 文件，了解识别过程中的详细参数
3. **问题定位**: 根据错误信息和参数值，定位识别失败的原因

### 调试建议

1. **角标检测失败**:
   - 检查 `debug_mark_mask.jpg` 确认颜色分割是否正确
   - 查看 `black_mark_debug.json` 中的轮廓信息
   - 调整HSV颜色范围或轮廓筛选标准

2. **网格映射错误**:
   - 检查 `debug_boardWarp.jpg` 确认棋盘矫正是否准确
   - 查看 `debug_overlay.jpg` 确认网格点是否对齐
   - 查看 `debug_corners.json` 中的映射坐标

3. **整体识别错误**:
   - 检查 `debug_source_corners.jpg` 确认棋盘四角定位
   - 综合分析所有调试文件，定位问题环节

---

## 配置文件

### FixedBoardCorners
定义不同分辨率的棋盘四角坐标（硬编码）：

```go
var FixedBoardCorners = map[string][]image.Point{
    "1200x2670": {
        {40, 536},
        {1160, 536},
        {1160, 1650},
        {40, 1650},
    },
}
```

### FixedBoardCropPercent
定义不同分辨率的裁剪比例：

```go
var FixedBoardCropPercent = map[string]CropPercent{
    "1200x2670": {Top: 0.0, Bottom: 0.0, Left: 0.0, Right: 0.0},
}
```

---

## 常见问题

**Q: 为什么只有黑棋才有 `black_mark_debug.json`？**

A: 因为黑棋使用红色角标，红色在HSV颜色空间中分布在两个区间，检测过程更复杂，需要更详细的调试信息。白棋使用蓝色角标，检测相对简单。

**Q: 如何调整红色角标检测的灵敏度？**

A: 修改 `FindMarkHSV` 函数中的HSV颜色范围或轮廓筛选标准，或调整 `SaveBlackMarkDebugInfo` 中的参数。

**Q: debug 文件会占用大量磁盘空间吗？**

A: 每个识别案例会生成约 5-6 个文件（4-5 张图片 + 1-2 个JSON），单个案例约 500KB-1MB。建议定期清理旧的 debug 文件。

---

## 更新日志

- **2026-02-05**: 添加 `black_mark_debug.json` 文件，记录黑棋红色角标识别的详细参数
