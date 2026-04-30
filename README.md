# Nozomi (希)

[![Go Version](https://img.shields.io/github/go-mod/go-version/Nozomi-Project/Nozomi)](https://golang.org)
[![Version](https://img.shields.io/badge/version-3.1.0-blue.svg)](https://github.com/Nozomi-Project/Nozomi/releases)

Nozomi is an engineering-focused AI Bot framework bridging **Matrix** and **Google Gemini**. Designed for stability, long-term interaction, and autonomous capability.

[中文文档](./README_ZH.md)

## 🚀 Quick Start

1.  **Configure**: Create and edit `configs/config.yaml` based on the template below.
2.  **Build**: 
    ```bash
    make build
    ```
3.  **Run**:
    ```bash
    ./dist/nozomi
    ```

## ⚡ Core Capabilities

### 1. Function Calling & Tool Use (v3.1.0)
- **Secure Terminal**: Executes bash commands with AST-based safety filtering. Dangerous commands (e.g., `rm`) require manual `/YES [task_id]` approval.
- **Cron Engine**: Schedule recurring LLM tasks directly via chat.
- **Grounding**: Seamless Google Search integration.

### 2. Memory Retrospection
Bypasses context limits by asynchronously summarizing old history when the buffer is full, maintaining a coherent long-term memory.

### 3. Perception & Reasoning
- **Multi-modal**: Native image/text mixed context support.
- **Thinking Mode**: Full extraction and display of Gemini's reasoning chains.

---

## 🔧 Configuration Guide

Detailed explanation of `configs/config.yaml`.

### `CLIENT` Section
| Key | Type | Description | Default/Example |
|:---|:---:|:---|:---|
| `homeserverURL` | `string` | The Matrix homeserver API endpoint. | `"https://matrix.org"` |
| `userID` | `string` | The full Matrix ID for the bot. | `"@nozomi:example.com"` |
| `accessToken` | `string` | Bot's access token for authentication. | (Obtain from Matrix client) |
| `deviceID` | `string` | Identifier for the current session. | `"Nozomi-Server"` |
| `logRoom` | `[]string`| Matrix Room IDs where system logs/errors are pushed. | `[]` |
| `maxMemoryLength` | `int` | Max message slots in the sliding window before summarization. | `14` |
| `whenRetroRemainMemLen`| `int` | Recent messages to keep intact after summarization. | `6` |
| `avatarURL` | `string` | MXC URI for the bot's profile picture. | `""` |
| `displayName` | `string` | Bot's display name in the Matrix room. | `"希"` |
| `databasePassword` | `string` | Password for SQLite/State DB encryption. | `"123456"` |

### `MODEL` Section
| Key | Type | Description | Default/Example |
|:---|:---:|:---|:---|
| `API_KEY` | `string` | Google AI Studio (Gemini) API Key. | (Your Key) |
| `model` | `string` | Specific Gemini model identifier. | `"gemini-3.1-flash-lite-preview"` |
| `prefixToCall` | `string` | Trigger prefix in group chats. | `"!c"` |
| `maxOutputToken` | `int` | Maximum tokens allowed in a single response. | `3000` |
| `alargmTokenCount` | `int` | Threshold to log a high-consumption warning. | `4000` |
| `useInternet` | `bool` | Enable/disable the Google Search tool. | `true` |
| `secureCheck` | `bool` | If `false`, sets safety filters to `BLOCK_NONE`. | `true` |
| `maxMonthlySearch` | `int` | Monthly quota for search tool calls. | `4000` |
| `timeOutWhen` | `string` | Hard timeout for LLM API calls. | `"30s"` |
| `includeThoughts` | `bool` | Whether to process/display the "Thinking" process. | `true` |
| `thinkingBudget` | `int` | Token budget for reasoning (0 for auto). | `0` |
| `thinkingLevel` | `string` | Reasoning depth (low/medium/high). | `"high"` |
| `rate` | `float` | Request rate limit per user (req/sec). | `0.20` |
| `rateBurst` | `int` | Maximum sudden burst of requests allowed. | `1` |

### `Auth` Section
| Key | Type | Description | Default/Example |
|:---|:---:|:---|:---|
| `adminID` | `[]string` | Users with permission to execute terminal commands. | `[]` |

---

## 🏗️ Architecture

- `internal/matrix`: Protocol sync and event orchestration.
- `internal/llm`: Gemini SDK integration and tool management.
- `internal/memory`: Async memory summarization logic.
- `internal/handler`: Central event routing and function execution.
- `internal/billing`: Token and quota persistence.

---
*Inspired by Nozomi from "Sonny Boy".*
