# Nozomi (希)

[![Go Version](https://img.shields.io/github/go-mod/go-version/Nozomi-Project/Nozomi)](https://golang.org)
[![Version](https://img.shields.io/badge/version-3.1.0-blue.svg)](https://github.com/Nozomi-Project/Nozomi/releases)

Nozomi 是一个面向工程化的 AI 机器人框架，深度整合 **Matrix** 协议与 **Google Gemini** 模型。专注于稳定性、长期记忆与自主工具调用能力。

[English Version](./README.md)

## 🚀 快速开始

1.  **配置**: 根据下文指南编辑 `configs/config.yaml`。
2.  **编译**: 
    ```bash
    make build
    ```
3.  **运行**:
    ```bash
    ./dist/nozomi
    ```

## ⚡ 核心能力

### 1. 函数调用与工具化 (v3.1.0)
- **安全终端**: 基于 AST 语法分析的 Bash 命令执行，危险操作（如 `rm`）需通过 `/YES [task_id]` 手动授权。
- **定时任务**: 直接在对话中创建并管理周期性 LLM 任务（如：每日简报）。
- **实时搜索**: 集成 Google Search，自动获取最新资讯。

### 2. 记忆回传算法
通过异步总结对话历史并回传上下文，在不超出窗口限制的前提下，维持长期的逻辑连贯性。

### 3. 感知与推理
- **多模态**: 原生支持图文混合输入。
- **思考模式**: 深度支持 Gemini 的推理过程提取与展示。

---

## 🔧 配置指南

`configs/config.yaml` 详细参数说明：

### `CLIENT` 部分 (客户端)
| 配置项 | 类型 | 说明 | 默认/示例 |
|:---|:---:|:---|:---|
| `homeserverURL` | `string` | Matrix 家园服务器地址。 | `"https://matrix.org"` |
| `userID` | `string` | 机器人的完整 Matrix ID。 | `"@nozomi:example.com"` |
| `accessToken` | `string` | 用于身份验证的访问令牌。 | (需从客户端获取) |
| `deviceID` | `string` | 当前登录设备的标识符。 | `"Nozomi-Server"` |
| `logRoom` | `[]string`| 系统日志和错误推送的房间 ID 列表。 | `[]` |
| `maxMemoryLength` | `int` | 滑动窗口最大消息数，超过则触发总结。 | `14` |
| `whenRetroRemainMemLen`| `int` | 记忆回传后保留的最近消息条数。 | `6` |
| `avatarURL` | `string` | 机器人头像的 MXC 链接。 | `""` |
| `displayName` | `string` | 机器人在 Matrix 房间显示的昵称。 | `"希"` |
| `databasePassword` | `string` | 状态数据库 (SQLite) 的加密密码。 | `"123456"` |

### `MODEL` 部分 (模型)
| 配置项 | 类型 | 说明 | 默认/示例 |
|:---|:---:|:---|:---|
| `API_KEY` | `string` | Google AI Studio (Gemini) API 密钥。 | (你的 API Key) |
| `model` | `string` | 使用的 Gemini 具体模型名称。 | `"gemini-3.1-flash-lite-preview"` |
| `prefixToCall` | `string` | 群聊中触发机器人的关键词前缀。 | `"!c"` |
| `maxOutputToken` | `int` | 单次回复生成的最大 Token 数。 | `3000` |
| `alargmTokenCount` | `int` | 高能耗报警阈值，超过此值会记录日志。 | `4000` |
| `useInternet` | `bool` | 是否启用 Google 搜索工具。 | `true` |
| `secureCheck` | `bool` | 若为 `false`，安全过滤器设为 `BLOCK_NONE`。 | `true` |
| `maxMonthlySearch` | `int` | 每月允许进行联网搜索的次数上限。 | `4000` |
| `timeOutWhen` | `string` | LLM API 调用的硬性超时时间。 | `"30s"` |
| `includeThoughts` | `bool` | 是否处理并展示模型的推理思考过程。 | `true` |
| `thinkingBudget` | `int` | 思考过程的 Token 预算 (0 为自动)。 | `0` |
| `thinkingLevel` | `string` | 推理深度等级 (low/medium/high)。 | `"high"` |
| `rate` | `float` | 单个用户每秒允许发送的请求数。 | `0.20` |
| `rateBurst` | `int` | 允许的单次突发请求最大数量。 | `1` |

### `Auth` 部分 (鉴权)
| 配置项 | 类型 | 说明 | 默认/示例 |
|:---|:---:|:---|:---|
| `adminID` | `[]string` | 拥有管理员权限（如执行终端工具）的用户 ID。 | `[]` |

---

## 🏗️ 技术架构

- `internal/matrix`: 协议同步与事件编排。
- `internal/llm`: Gemini SDK 集成与工具管理。
- `internal/memory`: 异步记忆总结逻辑。
- `internal/handler`: 中心事件路由与函数执行。
- `internal/billing`: Token 与配额的持久化统计。

---
*致敬《漂流少年》(Sonny Boy) 中的“希”。直面现实，不拘泥于形式。*
