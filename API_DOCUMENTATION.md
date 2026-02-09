# KaTrain HTTP API 接口文档

## 概述

KaTrain HTTP服务提供了一组RESTful接口，用于与KaTrain围棋程序进行交互。服务运行在 `http://localhost:8080`。

## 坐标系统

### 零基础坐标
- 坐标原点 (0, 0) 位于棋盘左上角
- x 轴：从左到右，范围 0-18
- y 轴：从上到下，范围 0-18

### GTP 坐标
- 横坐标：A-S（跳过I），对应 x 轴 0-18
- 纵坐标：1-19，对应 y 轴 18-0
- 格式：`{"x": "D", "y": 4}`

### 坐标转换示例
- 零基础坐标 (3, 15) → GTP 坐标 `{"x": "D", "y": 4}` （左上星位）
- 零基础坐标 (15, 15) → GTP 坐标 `{"x": "Q", "y": 4}` （右上星位）
- 零基础坐标 (3, 3) → GTP 坐标 `{"x": "D", "y": 16}` （左下星位）
- 零基础坐标 (15, 3) → GTP 坐标 `{"x": "Q", "y": 16}` （右下星位）

---

## 接口列表

### 1. 健康检查接口

**接口地址**：`GET /api/health`

**描述**：检查HTTP服务是否正常运行，以及是否能连接到KaTrain

**请求参数**：无

**响应示例**：
```json
{
  "success": true,
  "status": "ok"
}
```

**请求用例**：
```bash
curl http://localhost:8080/api/health
```

---

### 2. 检查坐标是否落子

**接口地址**：`GET /api/check-position`

**描述**：检查指定坐标是否已经落子

**请求参数**：
| 参数名 | 类型 | 必填 | 说明 | 示例 |
|--------|------|------|------|------|
| x | int | 是 | 横坐标（0-18） | 3 |
| y | int | 是 | 纵坐标（0-18） | 15 |

**响应示例**：

**有棋子的情况**：
```json
{
  "success": true,
  "has_stone": true,
  "player": "B",
  "coords": [3, 15],
  "gtp_coord": {
    "x": "D",
    "y": 4
  }
}
```

**无棋子的情况**：
```json
{
  "success": true,
  "has_stone": false,
  "player": null,
  "coords": [3, 15],
  "gtp_coord": {
    "x": "D",
    "y": 4
  }
}
```

**错误情况**：
```json
{
  "success": false,
  "error": "坐标超出棋盘范围"
}
```

**请求用例**：

1. 检查D4坐标（左上星位）：
```bash
curl "http://localhost:8080/api/check-position?x=3&y=15"
```

2. 检查Q16坐标（右下星位）：
```bash
curl "http://localhost:8080/api/check-position?x=15&y=3"
```

3. 检查中心点K10：
```bash
curl "http://localhost:8080/api/check-position?x=9&y=9"
```

---

### 3. 落子接口

**接口地址**：`POST /api/make-move`

**描述**：在指定坐标落子

**请求头**：
```
Content-Type: application/json
```

**请求参数**：
| 参数名 | 类型 | 必填 | 说明 | 示例 |
|--------|------|------|------|------|
| x | int | 是 | 横坐标（0-18） | 3 |
| y | int | 是 | 纵坐标（0-18） | 15 |
| player | string | 是 | 玩家颜色，"B" 或 "W" | "B" |

**响应示例**：

**成功落子**：
```json
{
  "success": true,
  "move": [3, 15],
  "gtp_coord": {
    "x": "D",
    "y": 4
  },
  "player": "B"
}
```

**错误情况**：

坐标已有棋子：
```json
{
  "success": false,
  "error": "该坐标已有棋子"
}
```

玩家颜色错误：
```json
{
  "success": false,
  "error": "玩家颜色必须是 B 或 W"
}
```

**请求用例**：

1. 在D4位置落黑棋：
```bash
curl -X POST http://localhost:8080/api/make-move \
  -H "Content-Type: application/json" \
  -d '{"x": 3, "y": 15, "player": "B"}'
```

2. 在Q16位置落白棋：
```bash
curl -X POST http://localhost:8080/api/make-move \
  -H "Content-Type: application/json" \
  -d '{"x": 15, "y": 3, "player": "W"}'
```

3. 在中心点K10落黑棋：
```bash
curl -X POST http://localhost:8080/api/make-move \
  -H "Content-Type: application/json" \
  -d '{"x": 9, "y": 9, "player": "B"}'
```

4. 使用Python请求：
```python
import requests
import json

url = "http://localhost:8080/api/make-move"
headers = {"Content-Type": "application/json"}
data = {
    "x": 3,
    "y": 15,
    "player": "B"
}

response = requests.post(url, headers=headers, json=data)
print(json.dumps(response.json(), indent=2))
```

5. 使用JavaScript请求：
```javascript
fetch('http://localhost:8080/api/make-move', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    x: 3,
    y: 15,
    player: 'B'
  })
})
.then(response => response.json())
.then(data => console.log(data));
```

---

### 4. 获取最后一手信息

**接口地址**：`GET /api/last-move`

**描述**：获取当前棋盘最后一手的信息，包括落子位置、玩家颜色和手数

**请求参数**：无

**响应示例**：

**有落子的情况**：
```json
{
  "success": true,
  "last_move": {
    "player": "B",
    "move_number": 1,
    "coords": [3, 15],
    "gtp_coord": {
      "x": "D",
      "y": 4
    }
  },
  "move_number": 1
}
```

**无落子的情况**：
```json
{
  "success": true,
  "last_move": null,
  "move_number": 0
}
```

**错误情况**：
```json
{
  "success": false,
  "error": "无法获取棋盘信息"
}
```

**请求用例**：

1. 获取最后一手：
```bash
curl http://localhost:8080/api/last-move
```

2. 使用Python请求：
```python
import requests
import json

url = "http://localhost:8080/api/last-move"
response = requests.get(url)
print(json.dumps(response.json(), indent=2))
```

---

## 完整使用示例

### 示例1：对局流程

```bash
# 1. 检查服务状态
curl http://localhost:8080/api/health

# 2. 检查D4位置是否为空
curl "http://localhost:8080/api/check-position?x=3&y=15"

# 3. 黑棋在D4落子
curl -X POST http://localhost:8080/api/make-move \
  -H "Content-Type: application/json" \
  -d '{"x": 3, "y": 15, "player": "B"}'

# 4. 获取最后一手信息
curl http://localhost:8080/api/last-move

# 5. 白棋在Q16落子
curl -X POST http://localhost:8080/api/make-move \
  -H "Content-Type: application/json" \
  -d '{"x": 15, "y": 3, "player": "W"}'

# 6. 再次获取最后一手信息
curl http://localhost:8080/api/last-move
```

### 示例2：Python完整示例

```python
import requests
import json
import time

BASE_URL = "http://localhost:8080"

def check_position(x, y):
    """检查坐标是否落子"""
    response = requests.get(f"{BASE_URL}/api/check-position", params={"x": x, "y": y})
    return response.json()

def make_move(x, y, player):
    """落子"""
    response = requests.post(
        f"{BASE_URL}/api/make-move",
        headers={"Content-Type": "application/json"},
        json={"x": x, "y": y, "player": player}
    )
    return response.json()

def get_last_move():
    """获取最后一手"""
    response = requests.get(f"{BASE_URL}/api/last-move")
    return response.json()

# 示例：对局流程
print("=== 对局开始 ===")

# 1. 检查服务状态
print("\n1. 检查服务状态...")
health = requests.get(f"{BASE_URL}/api/health").json()
print(f"服务状态: {health}")

# 2. 黑棋在D4落子
print("\n2. 黑棋在D4落子...")
result = make_move(3, 15, "B")
print(f"落子结果: {json.dumps(result, indent=2)}")

# 3. 获取最后一手
print("\n3. 获取最后一手...")
last_move = get_last_move()
print(f"最后一手: {json.dumps(last_move, indent=2)}")

# 4. 白棋在Q16落子
print("\n4. 白棋在Q16落子...")
result = make_move(15, 3, "W")
print(f"落子结果: {json.dumps(result, indent=2)}")

# 5. 再次获取最后一手
print("\n5. 获取最后一手...")
last_move = get_last_move()
print(f"最后一手: {json.dumps(last_move, indent=2)}")

print("\n=== 对局结束 ===")
```

### 示例3：JavaScript完整示例

```javascript
const BASE_URL = 'http://localhost:8080';

async function checkPosition(x, y) {
  const response = await fetch(`${BASE_URL}/api/check-position?x=${x}&y=${y}`);
  return await response.json();
}

async function makeMove(x, y, player) {
  const response = await fetch(`${BASE_URL}/api/make-move`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ x, y, player })
  });
  return await response.json();
}

async function getLastMove() {
  const response = await fetch(`${BASE_URL}/api/last-move`);
  return await response.json();
}

// 示例：对局流程
async function playGame() {
  console.log('=== 对局开始 ===');
  
  // 1. 检查服务状态
  console.log('\n1. 检查服务状态...');
  const health = await (await fetch(`${BASE_URL}/api/health`)).json();
  console.log('服务状态:', health);
  
  // 2. 黑棋在D4落子
  console.log('\n2. 黑棋在D4落子...');
  const move1 = await makeMove(3, 15, 'B');
  console.log('落子结果:', move1);
  
  // 3. 获取最后一手
  console.log('\n3. 获取最后一手...');
  const lastMove1 = await getLastMove();
  console.log('最后一手:', lastMove1);
  
  // 4. 白棋在Q16落子
  console.log('\n4. 白棋在Q16落子...');
  const move2 = await makeMove(15, 3, 'W');
  console.log('落子结果:', move2);
  
  // 5. 再次获取最后一手
  console.log('\n5. 获取最后一手...');
  const lastMove2 = await getLastMove();
  console.log('最后一手:', lastMove2);
  
  console.log('\n=== 对局结束 ===');
}

playGame();
```

---

## 错误码说明

| HTTP状态码 | 说明 |
|-----------|------|
| 200 | 请求成功 |
| 400 | 请求参数错误 |
| 404 | 接口不存在 |
| 500 | 服务器内部错误 |

---

## 注意事项

1. **坐标范围**：
   - 横坐标：0-18
   - 纵坐标：0-18
   - 超出范围会返回错误

2. **玩家颜色**：
   - 只能是 "B"（黑棋）或 "W"（白棋）
   - 其他值会返回错误

3. **落子规则**：
   - 不能在已有棋子的位置落子
   - 违反规则会返回错误

4. **跨域支持**：
   - 所有接口都支持跨域请求
   - 响应头包含 `Access-Control-Allow-Origin: *`

5. **服务依赖**：
   - HTTP服务需要KaTrain程序运行
   - KaTrain网络接口需要启动在 `localhost:12345`

---

## 启动服务

### 1. 启动KaTrain
```bash
cd /Users/chengjiahua/project/opensource/katrain-1.17.0
./start_katrain.sh
```

### 2. 启动HTTP服务
```bash
cd /Users/chengjiahua/project/opensource/katrain-1.17.0
python3 play_move_network.py
```

### 3. 验证服务
```bash
curl http://localhost:8080/api/health
```

---

## 常见问题

### Q: 如何停止HTTP服务？
A: 在运行HTTP服务的终端按 `Ctrl+C`。

### Q: 如何修改端口？
A: 修改 `play_move_network.py` 文件中的 `SERVER_PORT` 变量。

### Q: 如何查看KaTrain日志？
A: KaTrain日志会显示在启动KaTrain的终端中。

### Q: 接口返回的数据格式是什么？
A: 所有接口都返回JSON格式的数据。

### Q: 如何处理网络错误？
A: 检查KaTrain是否正常运行，网络接口是否启动在端口12345。

---

## 更新日志

- **v1.0** (2026-02-08)
  - 初始版本
  - 实现健康检查、坐标检查、落子、获取最后一手接口
  - 支持GTP坐标格式转换
  - 支持跨域请求