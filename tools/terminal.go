package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"slices"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/id"
)

type Task struct {
	RoomID   id.RoomID
	SenderID id.UserID
	TaskID   string
}

var TerminalTool = &genai.Tool{
	FunctionDeclarations: []*genai.FunctionDeclaration{
		{
			Name:        "execute_terminal",
			Description: "Executes Bash/Shell commands on the host system. Use only when the user explicitly requests to query system status, run scripts, or read local files.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "The complete terminal command to be executed, such as 'uname -a' or 'ls -l'.",
					},
				},
				Required: []string{"command"},
			},
		},
	},
}

// isDangerous 判断命令是否危险
func IsDangerous(script string) (isDanger bool, err error) {
	// 实例化 Parser
	parser := syntax.NewParser()
	reader := strings.NewReader(script)

	// 将文本解析为 AST (File 节点)
	ast, err := parser.Parse(reader, "")
	if err != nil {
		isDanger = false
		return isDanger, err
	}

	isDanger = false

	// 遍历 AST 的所有节点
	syntax.Walk(ast, func(node syntax.Node) bool {
		// 判断当前节点是否为命令调用 (CallExpr)
		if call, ok := node.(*syntax.CallExpr); ok {
			// 确保命令带有参数（第一个参数即为命令本身）
			if len(call.Args) > 0 {
				// 获取命令的字面量形式
				cmdName := call.Args[0].Lit()

				dangerStr := []string{
					// 1. 常见
					"rm", "rmdir", "kill", "shutdown", "mkfs", "sed", "drop", "curl", "wget", "reboot", "mv", "chmod", "dd", "chown", "cp", "ln",
					// 2. 权限提升
					"sudo", "su", "passwd", "chgrp", "usermod", "useradd", "adduser",
					// 3. 系统与进程
					"pkill", "killall", "systemctl", "service", "halt", "poweroff", "init", "ufw",
					// 4. 网络与传输
					"nc", "netcat", "socat", "ssh", "scp", "rsync", "ftp", "sftp",
					// 5. 磁盘操作
					"mount", "umount", "fdisk", "parted",
					// 6. 解释器与执行域绕过
					"eval", "exec", "source", "python", "python3", "perl", "ruby", "php", "node", "awk",
					// 7. 其他
					"crontab",
				}

				if slices.Contains(dangerStr, cmdName) {
					isDanger = true
					return false
				}
			}
		}
		return true
	})

	return isDanger, nil
}

func TryExecuteTerminal(command string, room id.RoomID, sender id.UserID) map[string]string {
	isDanger, err := IsDangerous(command)
	if isDanger || err != nil {
		b := make([]byte, 9)
		rand.Read(b)
		taskID := "cmd_" + hex.EncodeToString(b)

		m := map[string]string{
			"result":  "dangerous",
			"content": "[System Intercept: The command was marked as dangerous command and has been suspended.]",
			"task_id": taskID,
		}
		return m
	}

	return ExecuteTerminal(command)
}

func ExecuteTerminal(command string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	result := out.String()

	if ctx.Err() == context.DeadlineExceeded {
		result += "\n[System: Execution timed out after 15 seconds]"
		m := map[string]string{
			"result":  "timeout",
			"content": result,
		}
		return m
	}

	if err != nil {
		result += "\n[System Error: " + err.Error() + "]"
		m := map[string]string{
			"result":  "error",
			"content": result,
		}
		return m
	}

	if result == "" {
		m := map[string]string{
			"result":  "success",
			"content": "[System: Command executed successfully with no output]",
		}
		return m
	}

	maxLength := 4000
	if len(result) > maxLength {
		result = result[:maxLength] + "\n...[System: Output truncated due to length limits]"
	}

	return map[string]string{
		"result":  "success",
		"content": result,
	}
}
