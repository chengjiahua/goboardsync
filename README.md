# Go Game Sync - 手机与 KaTrain 双向同步工具

- 手机围棋棋盘与 KaTrain AI 之间的双向同步工具，自动识别落子位置并同步到 KaTrain。
- 让你能够在与人对战时，随时求助 AI 最犀利的招法。让你达到顶尖职业水准。网棋无敌的存在。
- 所有代码全部由 AI 生成，TRAE 国内版免费的 AI

## 功能特性

- **📱 手机 → KaTrain 同步**：通过 scrcpy 实时获取手机屏幕，识别最后一手位置，自动同步到 KaTrain
- **🖥️ KaTrain → 手机 同步**：轮询 KaTrain 最新落子，通过 ADB 在手机上模拟点击
- **🎯 自动角标识别**：基于颜色检测（红/蓝角标）自动识别最后一手，无需手动指定手数
- **🔄 双向同步**：支持手机和 KaTrain 之间的实时状态同步

## 系统架构

```
┌─────────────┐     scrcpy      ┌─────────────┐
│   手机设备   │ ──────────────→ │   本地程序   │
└─────────────┘                 └─────────────┘
                                      │
                                      │ HTTP API
                                      ↓
                              ┌─────────────┐
                              │  KaTrain   │
                              └─────────────┘
                                      ↑
                                      │ ADB 点击
                                      ↓
                              ┌─────────────┐
                              │   手机设备   │
                              └─────────────┘
```

## 成果展示



## 环境要求

- Go 1.20+
- scrcpy（用于手机投屏和截图）
- ADB（用于手机模拟点击）
- KaTrain + HTTP 服务

## 依赖安装

```bash
go mod tidy
```

安装 scrcpy：
```bash
# macOS
brew install scrcpy

# Linux
sudo apt install scrcpy

# Windows
# 下载地址: https://github.com/Genymobile/scrcpy
```

## 配置文件

在 `main.go` 中修改配置：

```go
const (
    WindowTitle   = "my_phone"           // scrcpy 窗口标题
    Interval      = 1000 * time.Millisecond  // 截图间隔
    ImageDir      = "/Users/chengjiahua/project/my-app"  // 截图保存路径
    TempImage     = "/Users/chengjiahua/project/my-app/screenshot.jpg"
    TargetW       = 1200                  // 手机分辨率宽度
    TargetH       = 2670                  // 手机分辨率高度
    POLL_INTERVAL = 100 * time.Millisecond  // KaTrain 轮询间隔
)

var (
    KATRAIN_URL = "http://localhost:8080"  // KaTrain API 地址
)
```

## 运行步骤

### 1. 启动 KaTrain HTTP 服务

```bash
cd /Users/chengjiahua/project/opensource/katrain-1.17.0
python3 play_move_network.py
```

### 2. 启动本程序

```bash
cd /Users/chengjiahua/project/my-app
go run main.go
```

程序启动后会：
1. 清空 KaTrain 棋盘
2. 启动 scrcpy 进行手机投屏
3. 启动双向同步协程

## 项目结构

```
my-app/
├── main.go              # 主程序入口
├── main_test.go         # 主程序单元测试
├── API_DOCUMENTATION.md # KaTrain API 文档
├── go.mod               # Go 依赖
├── go.sum               # Go 依赖校验
├── images/              # 测试图片样本
└── vision/
    ├── detector.go      # 视觉识别核心算法
    └── detector_test.go # 视觉识别单元测试
```

## 核心模块

### vision/detector.go

视觉识别模块，包含以下核心函数：

| 函数 | 功能 |
|-----|------|
| `DetectLastMoveCoord(img)` | 自动检测最后一手位置和颜色 |
| `findRedMarker(img)` | 检测红色角标（黑棋） |
| `findBlueMarker(img)` | 检测蓝色角标（白棋） |
| `WarpBoard(img, corners)` | 透视变换提取棋盘区域 |
| `FetchMoveNumberFromOCR(img)` | OCR 识别手数 |

### 主程序功能

| 函数 | 功能 |
|-----|------|
| `syncPhoneToKatrain()` | 手机 → KaTrain 同步 |
| `syncKatrainToPhone()` | KaTrain → 手机 同步 |
| `checkPosition(x, y)` | 检查坐标是否有棋子 |
| `makeMove(x, y, player)` | 在 KaTrain 落子 |
| `getLastMove()` | 获取 KaTrain 最后一手 |
| `resetKatrainBoard()` | 重置 KaTrain 棋盘 |
| `tapOnPhone(x, y)` | 在手机对应位置点击 |
| `captureWithADB()` | 通过 ADB 截图 |
| `recognizeWithVision(path)` | 视觉识别 |

## 日志输出

程序运行时会输出同步日志：

```
🚀 程序已启动
   监控窗口: my_phone
   截图保存路径: /Users/chengjiahua/project/my-app/screenshot.jpg
   KaTrain API: http://localhost:8080
   屏幕分辨率: 1200x2670
   按 Ctrl+C 停止程序
============================================================
[15:04:05] 🔄 启动双向同步...
[15:04:05] 📱 监听手机 → KaTrain
[15:04:05] 🖥️  监听 KaTrain → 手机
[15:04:05] 🧹 正在清空 KaTrain 棋盘...
[15:04:05] ✅ KaTrain 棋盘已清空
[15:04:05] 📸 截图成功: /Users/chengjiahua/project/my-app/screenshot.jpg
[15:04:05] ✅ 识别成功: 第 7 手, 坐标: 3-15, 颜色: B
[15:04:05] 🔄 检测到新手: 7 > 0  X:3  Y:15
[15:04:05] ✅ 手机→KaTrain: 第 7 手 黑棋 D16
```

## 单元测试

运行所有测试：

```bash
go test -v ./...
```

测试文件：

- `main_test.go`：测试 KaTrain API 客户端功能
- `vision/detector_test.go`：测试视觉识别算法

## 技术栈

- **Go**：主开发语言
- **gocv**：OpenCV Go 接口，用于图像处理
- **scrcpy**：手机投屏和截图
- **ADB**：Android 调试桥，用于模拟点击
- **HTTP/REST**：与 KaTrain 通信

## 注意事项

1. 确保 KaTrain HTTP 服务已启动（默认 `localhost:8080`）
2. 确保手机已连接 ADB
3. scrcpy 窗口标题需与配置一致（默认 `my_phone`）
4. 分辨率配置需与实际手机屏幕匹配

## 许可证

MIT License
