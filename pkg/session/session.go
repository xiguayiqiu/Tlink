package session

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/pterm/pterm"

	"tlink/pkg/protocol"
	"tlink/pkg/transfer"
)

const (
	CHUNK_SIZE = 64 * 1024
)

type Session struct {
	uid            string
	deviceName     string
	conn           net.Conn
	peerName       string
	saveDir        string
	stopChan       chan struct{}
	isTransmitting bool
	transmitChan   chan *protocol.Message
	interruptChan  chan os.Signal
	cancelChan     chan struct{}
	initialFile    string
	compress       bool
}

func NewSession(uid, deviceName, saveDir string, conn net.Conn, peerName string, initialFile ...string) *Session {
	s := &Session{
		uid:           uid,
		deviceName:    deviceName,
		conn:          conn,
		peerName:      peerName,
		saveDir:       saveDir,
		stopChan:      make(chan struct{}),
		transmitChan:  make(chan *protocol.Message, 10),
		interruptChan: make(chan os.Signal, 1),
	}
	if len(initialFile) > 0 {
		s.initialFile = initialFile[0]
	}
	return s
}

func (s *Session) SetCompress(enabled bool) {
	s.compress = enabled
}

func (s *Session) Start() {
	pterm.DefaultBox.WithTitle("✓ 连接成功!").Println()
	fmt.Println()
	pterm.Success.Printf("已连接到 %s\n", s.peerName)
	fmt.Println()
	pterm.Info.Println("输入 'help' 或 '--help' 查看可用命令")
	fmt.Println()

	// 注册信号处理
	signal.Notify(s.interruptChan, os.Interrupt, syscall.SIGTERM)

	// 启动信号监听 goroutine
	go func() {
		for {
			select {
			case <-s.interruptChan:
				fmt.Println()
				if s.isTransmitting {
					// 正在传输，取消当前传输
					pterm.Warning.Println("\n收到中断信号，正在取消传输...")
					if s.cancelChan != nil {
						close(s.cancelChan)
					}
				} else {
					// 没有传输，退出程序
					pterm.Warning.Println("\n收到中断信号，正在退出...")
					close(s.stopChan)
					return
				}
			case <-s.stopChan:
				return
			}
		}
	}()

	// 启动消息接收 goroutine
	go s.receiveMessages()

	// 如果有初始文件，先自动发送
	if s.initialFile != "" {
		pterm.Info.Printf("正在自动发送文件: %s\n", s.initialFile)
		s.sendFile(s.initialFile)
	}

	// 启动交互式命令行
	s.runInteractive()
}

func (s *Session) runInteractive() {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "[tlink]> ",
		HistoryFile:     filepath.Join(os.TempDir(), "tlink_history"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		// 如果 readline 初始化失败，回退到简单的 bufio
		s.runInteractiveFallback()
		return
	}
	defer rl.Close()

	// 启动 goroutine 检查 stopChan
	stopReadChan := make(chan struct{})
	go func() {
		select {
		case <-s.stopChan:
			rl.Clean()
			close(stopReadChan)
		}
	}()

	for {
		select {
		case <-stopReadChan:
			fmt.Println()
			pterm.Warning.Println("连接已断开！")
			return
		default:
			input, err := rl.Readline()
			if err != nil {
				if err == readline.ErrInterrupt {
					// Ctrl+C 只是中断当前，不退出
					continue
				}
				if err == io.EOF {
					pterm.Info.Println("正在退出...")
					close(s.stopChan)
					return
				}
				pterm.Error.Printf("读取命令失败: %v\n", err)
				continue
			}

			input = strings.TrimSpace(input)
			if input == "" {
				continue
			}

			// 添加到历史记录
			rl.SaveHistory(input)

			parts := strings.Fields(input)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "help", "--help":
				s.printHelp()
			case "exit", "quit":
				pterm.Info.Println("正在退出...")
				close(s.stopChan)
				return
			case "send":
				if len(parts) < 2 {
					pterm.Error.Println("用法: send <文件路径>")
					continue
				}
				filePath := parts[1]
				s.sendFile(filePath)
			default:
				s.executeSystemCommand(input)
			}
		}
	}
}

func (s *Session) runInteractiveFallback() {
	reader := bufio.NewReader(os.Stdin)

	for {
		select {
		case <-s.stopChan:
			fmt.Println()
			pterm.Warning.Println("连接已断开！")
			return
		default:
			fmt.Printf("[tlink]> ")
			input, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					pterm.Error.Printf("读取命令失败: %v\n", err)
				}
				break
			}

			input = strings.TrimSpace(input)
			if input == "" {
				continue
			}

			parts := strings.Fields(input)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "help", "--help":
				s.printHelp()
			case "exit", "quit":
				pterm.Info.Println("正在退出...")
				close(s.stopChan)
				return
			case "send":
				if len(parts) < 2 {
					pterm.Error.Println("用法: send <文件路径>")
					continue
				}
				filePath := parts[1]
				s.sendFile(filePath)
			default:
				s.executeSystemCommand(input)
			}
		}
	}
}

func (s *Session) printHelp() {
	fmt.Println()
	pterm.DefaultBox.WithTitle("TLink 交互式会话帮助").Println()
	fmt.Println()
	pterm.Info.Println("可用命令:")
	fmt.Println()
	pterm.FgLightCyan.Println("  📤 send <文件路径>")
	fmt.Println("     发送指定文件到对方")
	fmt.Println()
	pterm.FgLightCyan.Println("  📖 help / --help")
	fmt.Println("     显示此帮助信息")
	fmt.Println()
	pterm.FgLightCyan.Println("  👋 exit / quit")
	fmt.Println("     退出当前会话")
	fmt.Println()
	pterm.FgLightCyan.Println("  💻 <其他任何命令>")
	fmt.Println("     直接执行系统shell命令")
	fmt.Println()
	pterm.DefaultBox.Printfln("提示: 连接后双方都可以发送和接收文件")
	fmt.Println()
}

func (s *Session) sendFile(filePath string) {
	// 检查是否正在传输
	if s.isTransmitting {
		pterm.Warning.Println("正在传输文件，请稍后再试")
		return
	}

	// 清理路径：移除末尾的斜杠
	filePath = filepath.Clean(filePath)

	// 清空传输通道中的旧消息
	s.clearTransmitChan()

	// 创建取消通道
	s.cancelChan = make(chan struct{})

	// 标记为正在传输
	s.isTransmitting = true
	defer func() {
		s.isTransmitting = false
		s.cancelChan = nil
	}()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		pterm.Error.Printf("无法访问文件: %v\n", err)
		return
	}

	// 处理目录
	var actualPath string
	var isCompressed bool
	if fileInfo.IsDir() {
		if !s.compress {
			pterm.Warning.Println("检测到目录，自动启用压缩传输...")
		}
		// 压缩目录
		zipPath := filePath + ".zip"
		pterm.Info.Printf("正在压缩目录: %s -> %s\n", filePath, zipPath)
		if err := compressDirectory(filePath, zipPath); err != nil {
			pterm.Error.Printf("压缩目录失败: %v\n", err)
			return
		}
		defer os.Remove(zipPath) // 传输完后删除临时文件
		actualPath = zipPath
		isCompressed = true
		fileInfo, err = os.Stat(zipPath)
		if err != nil {
			pterm.Error.Printf("无法访问压缩文件: %v\n", err)
			return
		}
	} else if s.compress {
		// 单个文件也压缩
		zipPath := filePath + ".zip"
		pterm.Info.Printf("正在压缩文件: %s -> %s\n", filePath, zipPath)
		if err := compressDirectory(filePath, zipPath); err != nil {
			pterm.Error.Printf("压缩文件失败: %v\n", err)
			return
		}
		defer os.Remove(zipPath)
		actualPath = zipPath
		isCompressed = true
		fileInfo, err = os.Stat(zipPath)
		if err != nil {
			pterm.Error.Printf("无法访问压缩文件: %v\n", err)
			return
		}
	} else {
		actualPath = filePath
		isCompressed = false
	}

	file, err := os.Open(actualPath)
	if err != nil {
		pterm.Error.Printf("无法打开文件: %v\n", err)
		return
	}
	defer file.Close()

	fileHash, err := transfer.ComputeFileHash(file)
	if err != nil {
		pterm.Warning.Printf("无法计算文件哈希: %v\n", err)
	}
	file.Seek(0, 0)

	metadata := protocol.FileMetadataData{
		FileName:     filepath.Base(filePath),
		FileSize:     fileInfo.Size(),
		LastModified: fileInfo.ModTime().Unix(),
		FileHash:     fileHash,
		IsCompressed: isCompressed,
	}

	msg := protocol.NewMessage(protocol.MsgTypeFileMetadata, metadata)
	if err := sendMessage(s.conn, msg); err != nil {
		pterm.Error.Printf("发送文件元数据失败: %v\n", err)
		return
	}

	// 等待确认，但同时检查取消
	select {
	case <-s.cancelChan:
		// 发送取消消息给对方
		cancelMsg := protocol.NewMessage(protocol.MsgTypeCancelTransfer, nil)
		sendMessage(s.conn, cancelMsg)
		pterm.Warning.Println("发送已取消")
		return
	case ackMsg, ok := <-s.readTransmitMessageWithCancel():
		if !ok {
			pterm.Warning.Println("发送已取消")
			return
		}
		if ackMsg.Type != protocol.MsgTypeMetadataAck {
			pterm.Error.Printf("收到意外消息: %s\n", ackMsg.Type)
			return
		}
	}

	pterm.Info.Printf("正在发送文件: %s (%s)\n", metadata.FileName, protocol.FormatSize(metadata.FileSize))

	startTime := time.Now()
	buffer := make([]byte, CHUNK_SIZE)
	chunkIndex := 0
	totalSent := int64(0)

	progressBar, _ := pterm.DefaultProgressbar.
		WithTotal(100).
		WithShowCount(true).
		WithShowTitle(true).
		WithTitle("发送进度").
		WithBarStyle(pterm.NewStyle(pterm.FgMagenta)).
		WithTitleStyle(pterm.NewStyle(pterm.FgLightMagenta)).
		Start()

	var lastPercent int
	cancelled := false

	for {
		// 检查是否已取消
		select {
		case <-s.cancelChan:
			cancelled = true
			break
		default:
		}
		if cancelled {
			break
		}

		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			progressBar.Stop()
			pterm.Error.Printf("读取文件失败: %v\n", err)
			return
		}
		if n == 0 {
			break
		}

		chunkData := protocol.FileChunkData{
			ChunkIndex: chunkIndex,
			Data:       buffer[:n],
			ChunkHash:  transfer.ComputeHash(buffer[:n]),
		}

		chunkMsg := protocol.NewMessage(protocol.MsgTypeFileChunk, chunkData)
		if err := sendMessage(s.conn, chunkMsg); err != nil {
			progressBar.Stop()
			pterm.Error.Printf("发送文件块失败: %v\n", err)
			return
		}

		totalSent += int64(n)

		percent := float64(totalSent) / float64(metadata.FileSize) * 100
		currentPercent := int(percent)
		if currentPercent != lastPercent && currentPercent <= 100 {
			delta := currentPercent - lastPercent
			if delta > 0 {
				elapsed := time.Since(startTime)
				speed := float64(totalSent) / elapsed.Seconds()
				speedText := protocol.FormatSize(int64(speed))
				progressBar.UpdateTitle(fmt.Sprintf("发送进度 | %s/s", speedText))
				progressBar.Add(delta)
				lastPercent = currentPercent
			}
		}

		chunkIndex++
	}

	progressBar.Stop()

	if cancelled {
		// 发送取消消息给对方
		cancelMsg := protocol.NewMessage(protocol.MsgTypeCancelTransfer, nil)
		sendMessage(s.conn, cancelMsg)
		pterm.Warning.Println("文件发送已取消")
		return
	}

	completeMsg := protocol.NewMessage(protocol.MsgTypeTransferComplete, nil)
	if err := sendMessage(s.conn, completeMsg); err != nil {
		pterm.Error.Printf("发送完成消息失败: %v\n", err)
		return
	}

	pterm.Success.Println("文件发送成功！")
	elapsed := time.Since(startTime)
	avgSpeed := float64(metadata.FileSize) / elapsed.Seconds()
	pterm.Info.Printf("耗时: %s, 平均速度: %s/s\n", formatDuration(elapsed), protocol.FormatSize(int64(avgSpeed)))
	fmt.Println()
}

func (s *Session) receiveMessages() {
	for {
		select {
		case <-s.stopChan:
			return
		default:
			msg, err := readMessage(s.conn)
			if err != nil {
				if err != io.EOF {
					pterm.Error.Printf("\n接收消息失败: %v\n", err)
				}
				fmt.Println()
				pterm.Warning.Println("连接已断开！")
				close(s.stopChan)
				return
			}

			// 检查是否正在传输
			if s.isTransmitting {
				if msg.Type == protocol.MsgTypeCancelTransfer {
					// 对方取消了传输，通知当前传输
					if s.cancelChan != nil {
						close(s.cancelChan)
					}
				} else {
					// 其他传输相关消息，发送到传输通道
					s.transmitChan <- msg
				}
			} else {
				// 如果没有传输，处理非传输消息
				switch msg.Type {
				case protocol.MsgTypeFileMetadata:
					// 再次检查是否正在传输（防止竞态条件）
					if s.isTransmitting {
						// 已经在传输中，忽略这个请求
						pterm.Warning.Println("\n收到文件传输请求，但当前正在传输中，忽略")
						fmt.Printf("[tlink]> ")
					} else {
						// 立即设置传输状态，防止后续消息被错误处理
						s.isTransmitting = true
						// 在 goroutine 中处理文件传输
						go s.handleIncomingFile(msg)
					}
				case protocol.MsgTypeCancelTransfer:
					// 收到取消传输但没有正在传输，忽略
				case protocol.MsgTypeTransferComplete:
					// 收到完成但没有正在传输，忽略（可能是旧消息）
				default:
					pterm.Warning.Printf("\n收到未知消息类型: %s\n", msg.Type)
					fmt.Printf("[tlink]> ")
				}
			}
		}
	}
}

func (s *Session) readTransmitMessage() (*protocol.Message, error) {
	select {
	case msg := <-s.transmitChan:
		return msg, nil
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("等待消息超时")
	case <-s.stopChan:
		return nil, fmt.Errorf("连接已断开")
	}
}

// clearTransmitChan 清空传输通道中的旧消息
func (s *Session) clearTransmitChan() {
	for {
		select {
		case <-s.transmitChan:
			// 清空旧消息
		default:
			return
		}
	}
}

func (s *Session) readTransmitMessageWithCancel() <-chan *protocol.Message {
	resultChan := make(chan *protocol.Message, 1)

	go func() {
		select {
		case msg := <-s.transmitChan:
			resultChan <- msg
		case <-s.cancelChan:
			close(resultChan)
		case <-s.stopChan:
			close(resultChan)
		}
	}()

	return resultChan
}

func (s *Session) handleIncomingFile(initMsg *protocol.Message) {
	// 标记为正在传输的状态已经在 receiveMessages 中设置
	// 清空传输通道中的旧消息
	s.clearTransmitChan()

	// 创建取消通道
	s.cancelChan = make(chan struct{})

	cancelled := false
	savePath := ""     // 最终保存路径
	tempSavePath := "" // 临时接收路径
	var file *os.File
	var tempZipPath string

	defer func() {
		s.isTransmitting = false
		s.cancelChan = nil
		if file != nil {
			file.Close()
		}
		// 如果被取消，删除不完整的文件
		if cancelled && tempSavePath != "" {
			os.Remove(tempSavePath)
		}
		if tempZipPath != "" {
			os.Remove(tempZipPath)
		}
	}()

	metadata, err := protocol.ParseFileMetadataData(initMsg.Data)
	if err != nil {
		pterm.Error.Printf("\n解析文件元数据失败: %v\n", err)
		fmt.Printf("[tlink]> ")
		return
	}

	ackMsg := protocol.NewMessage(protocol.MsgTypeMetadataAck, nil)
	if err := sendMessage(s.conn, ackMsg); err != nil {
		pterm.Error.Printf("\n发送确认失败: %v\n", err)
		fmt.Printf("[tlink]> ")
		return
	}

	fmt.Println()
	if metadata.IsCompressed {
		pterm.Warning.Printf("\n收到压缩文件传输请求: %s (%s)\n", metadata.FileName, protocol.FormatSize(metadata.FileSize))
	} else {
		pterm.Warning.Printf("\n收到文件传输请求: %s (%s)\n", metadata.FileName, protocol.FormatSize(metadata.FileSize))
	}
	pterm.Info.Println("正在接收文件...")

	savePath = filepath.Join(s.saveDir, metadata.FileName)
	// 全部先保存到 .tmp 临时文件，传输完成后再重命名，避免 Windows 文件占用问题
	if metadata.IsCompressed {
		tempZipPath = filepath.Join(s.saveDir, metadata.FileName+".tmp.zip")
		file, err = os.Create(tempZipPath)
	} else {
		tempSavePath = filepath.Join(s.saveDir, metadata.FileName+".tmp")
		file, err = os.Create(tempSavePath)
	}
	if err != nil {
		pterm.Error.Printf("\n创建文件失败: %v\n", err)
		fmt.Printf("[tlink]> ")
		return
	}

	startTime := time.Now()
	totalReceived := int64(0)
	expectedChunks := (metadata.FileSize + CHUNK_SIZE - 1) / CHUNK_SIZE
	receivedChunks := 0

	progressBar, _ := pterm.DefaultProgressbar.
		WithTotal(100).
		WithShowCount(true).
		WithShowTitle(true).
		WithTitle("接收进度").
		WithBarStyle(pterm.NewStyle(pterm.FgGreen)).
		WithTitleStyle(pterm.NewStyle(pterm.FgLightGreen)).
		Start()

	var lastPercent int

	for receivedChunks < int(expectedChunks) {
		// 检查是否已取消
		select {
		case <-s.cancelChan:
			cancelled = true
			break
		default:
		}
		if cancelled {
			break
		}

		chunkMsg, err := s.readTransmitMessage()
		if err != nil {
			progressBar.Stop()
			pterm.Error.Printf("\n接收文件块失败: %v\n", err)
			fmt.Printf("[tlink]> ")
			return
		}

		if chunkMsg.Type == protocol.MsgTypeTransferComplete {
			break
		}

		if chunkMsg.Type != protocol.MsgTypeFileChunk {
			progressBar.Stop()
			pterm.Error.Printf("\n收到意外消息: %s\n", chunkMsg.Type)
			fmt.Printf("[tlink]> ")
			return
		}

		chunkData, err := protocol.ParseFileChunkData(chunkMsg.Data)
		if err != nil {
			progressBar.Stop()
			pterm.Error.Printf("\n解析文件块失败: %v\n", err)
			fmt.Printf("[tlink]> ")
			return
		}

		if chunkData.ChunkHash != "" {
			computedHash := transfer.ComputeHash(chunkData.Data)
			if computedHash != chunkData.ChunkHash {
				progressBar.Stop()
				pterm.Error.Printf("\n文件块 %d 哈希不匹配\n", chunkData.ChunkIndex)
				fmt.Printf("[tlink]> ")
				return
			}
		}

		if _, err := file.Write(chunkData.Data); err != nil {
			progressBar.Stop()
			pterm.Error.Printf("\n写入文件失败: %v\n", err)
			fmt.Printf("[tlink]> ")
			return
		}

		totalReceived += int64(len(chunkData.Data))

		percent := float64(totalReceived) / float64(metadata.FileSize) * 100
		currentPercent := int(percent)
		if currentPercent != lastPercent && currentPercent <= 100 {
			delta := currentPercent - lastPercent
			if delta > 0 {
				elapsed := time.Since(startTime)
				speed := float64(totalReceived) / elapsed.Seconds()
				speedText := protocol.FormatSize(int64(speed))
				progressBar.UpdateTitle(fmt.Sprintf("接收进度 | %s/s", speedText))
				progressBar.Add(delta)
				lastPercent = currentPercent
			}
		}

		receivedChunks++
	}

	progressBar.Stop()

	if cancelled {
		// 发送取消消息给对方
		cancelMsg := protocol.NewMessage(protocol.MsgTypeCancelTransfer, nil)
		sendMessage(s.conn, cancelMsg)
		pterm.Warning.Println("\n文件接收已取消，删除不完整文件")
		fmt.Printf("[tlink]> ")
		return
	}

	// 计算哈希前先关闭文件，因为 Windows 上正在打开的文件可能有问题
	file.Close()
	file = nil

	// 如果不是压缩文件，先重命名临时文件到正式文件名
	var hashFile *os.File
	if !metadata.IsCompressed {
		// 重命名临时文件
		if err := os.Rename(tempSavePath, savePath); err != nil {
			// 如果重命名失败（比如 Windows 上目标文件正在运行），给用户提示
			pterm.Warning.Printf("\n无法覆盖目标文件（可能正在使用中）: %v\n临时文件已保存: %s\n", err, tempSavePath)
		} else {
			// 重命名成功，清理 tempSavePath
			tempSavePath = ""
		}
		// 打开正式文件计算哈希
		if tempSavePath == "" {
			hashFile, err = os.Open(savePath)
		} else {
			hashFile, err = os.Open(tempSavePath)
		}
	} else {
		// 压缩文件的话，使用临时 zip 文件计算哈希
		hashFile, err = os.Open(tempZipPath)
	}
	if err != nil {
		pterm.Warning.Printf("\n无法打开文件计算哈希: %v\n", err)
	} else {
		if metadata.FileHash != "" {
			computedFileHash, err := transfer.ComputeFileHash(hashFile)
			hashFile.Close()
			if err != nil {
				pterm.Warning.Printf("\n无法计算文件哈希: %v\n", err)
			} else if computedFileHash == metadata.FileHash {
				pterm.Success.Println("\n✓ 文件完整性验证成功")
			} else {
				pterm.Error.Println("\n✗ 文件完整性验证失败")
			}
		} else {
			hashFile.Close()
		}
	}

	pterm.Success.Println("\n文件接收成功！")

	// 如果是压缩文件，进行解压
	if metadata.IsCompressed {
		pterm.Info.Println("正在解压文件...")
		destPath := filepath.Join(s.saveDir, metadata.FileName)
		if err := unzipArchive(tempZipPath, s.saveDir); err != nil {
			pterm.Warning.Printf("解压失败: %v，压缩文件已保留: %s\n", err, tempZipPath)
			// 不设置 tempZipPath = ""，保留文件不删除
		} else {
			pterm.Success.Println("解压成功！")
			// 解压成功后删除临时 zip
			if tempZipPath != "" {
				os.Remove(tempZipPath)
			}
		}
		if _, err := os.Stat(tempZipPath); os.IsNotExist(err) {
			pterm.Info.Printf("保存到: %s\n", destPath)
		} else {
			pterm.Info.Printf("压缩文件已保留: %s\n", tempZipPath)
		}
	} else {
		if tempSavePath == "" {
			pterm.Info.Printf("保存到: %s\n", savePath)
		} else {
			pterm.Info.Printf("临时保存到: %s\n请手动重命名为: %s\n", tempSavePath, savePath)
		}
	}

	elapsed := time.Since(startTime)
	avgSpeed := float64(metadata.FileSize) / elapsed.Seconds()
	pterm.Info.Printf("耗时: %s, 平均速度: %s/s\n", formatDuration(elapsed), protocol.FormatSize(int64(avgSpeed)))
	fmt.Printf("[tlink]> ")

	// 清空传输通道中的剩余消息，防止后续警告
	s.clearTransmitChan()
}

func (s *Session) executeSystemCommand(cmdStr string) {
	var cmd *exec.Cmd

	// 尝试使用标准库 exec，而不是依赖 shellexec
	// 根据不同操作系统选择 shell
	if runtime.GOOS == "windows" {
		// Windows 上使用 cmd.exe
		cmd = exec.Command("cmd.exe", "/c", cmdStr)
	} else {
		// Linux/macOS 上使用 bash
		cmd = exec.Command("bash", "-c", cmdStr)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		// 检测特殊错误
		errStr := fmt.Sprintf("%v", err)
		if strings.Contains(errStr, "SIGSYS") || strings.Contains(errStr, "syscall") {
			pterm.Warning.Println("Termux 环境不支持系统命令执行")
			pterm.Info.Println("在 Termux 中请直接使用 send 命令传输文件")
			return
		}
		if runtime.GOOS == "windows" {
			pterm.Warning.Println("Windows 上系统命令执行受限")
			pterm.Info.Println("建议直接使用 send 命令传输文件")
			return
		}
		pterm.Error.Printf("执行命令失败: %v\n", err)
		return
	}

	err = cmd.Wait()
	if err != nil {
		errStr := fmt.Sprintf("%v", err)
		if strings.Contains(errStr, "SIGSYS") || strings.Contains(errStr, "syscall") {
			pterm.Warning.Println("Termux 环境不支持系统命令执行")
			pterm.Info.Println("在 Termux 中请直接使用 send 命令传输文件")
			return
		}
		// 在 Windows 上，如果退出代码非 0，可能是命令不存在
		if runtime.GOOS == "windows" && strings.Contains(errStr, "exit status") {
			// 安静地忽略
		} else {
			pterm.Warning.Printf("命令执行完成，退出代码: %v\n", err)
		}
		return
	}
}

func sendMessage(conn net.Conn, msg *protocol.Message) error {
	data, err := msg.ToJSON()
	if err != nil {
		return err
	}

	length := uint32(len(data))
	lengthBytes := make([]byte, 4)
	lengthBytes[0] = byte(length >> 24)
	lengthBytes[1] = byte(length >> 16)
	lengthBytes[2] = byte(length >> 8)
	lengthBytes[3] = byte(length)

	if _, err := conn.Write(lengthBytes); err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func readMessage(conn net.Conn) (*protocol.Message, error) {
	lengthBytes := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBytes); err != nil {
		return nil, err
	}

	length := uint32(lengthBytes[0])<<24 | uint32(lengthBytes[1])<<16 | uint32(lengthBytes[2])<<8 | uint32(lengthBytes[3])

	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	return protocol.FromJSON(data)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%d小时%d分%d秒", h, m, s)
	} else if m > 0 {
		return fmt.Sprintf("%d分%d秒", m, s)
	}
	return fmt.Sprintf("%d秒", s)
}

func compressDirectory(path string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	baseName := filepath.Base(path)

	if info.IsDir() {
		// 如果是目录，遍历目录内容，相对于当前目录
		err = filepath.Walk(path, func(curPath string, curInfo fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(path, curPath)
			if err != nil {
				return err
			}

			// 构建在 zip 中的路径：baseName/relPath
			zipEntryPath := filepath.Join(baseName, relPath)

			if curInfo.IsDir() {
				// 创建目录条目，使用 / 作为路径分隔符（zip 标准）
				zipEntryPath = strings.ReplaceAll(zipEntryPath, "\\", "/") + "/"
				if _, err := w.Create(zipEntryPath); err != nil {
					return err
				}
				return nil
			}

			// 创建文件条目，使用 / 作为路径分隔符
			zipEntryPath = strings.ReplaceAll(zipEntryPath, "\\", "/")
			f, err := w.Create(zipEntryPath)
			if err != nil {
				return err
			}

			file, err := os.Open(curPath)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(f, file)
			return err
		})
	} else {
		// 单个文件
		zipEntryPath := strings.ReplaceAll(baseName, "\\", "/")
		f, err := w.Create(zipEntryPath)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(f, file)
	}

	return err
}

func unzipArchive(zipPath string, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	var failedFiles []string // 记录解压失败的文件

	for _, f := range r.File {
		// 清理 zip 中的路径，将 / 转换为当前系统的分隔符
		cleanName := filepath.Clean(f.Name)
		// 防止路径穿越（如 ../）
		if strings.HasPrefix(cleanName, "..") || strings.HasPrefix(cleanName, string(os.PathSeparator)) {
			failedFiles = append(failedFiles, f.Name+": 不安全的路径")
			continue
		}

		fpath := filepath.Join(destDir, cleanName)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, 0755); err != nil {
				failedFiles = append(failedFiles, f.Name+": "+err.Error())
				continue
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			failedFiles = append(failedFiles, f.Name+": "+err.Error())
			continue
		}

		// 先写到临时文件，用足够的权限创建
		tempPath := fpath + ".tmp"
		outFile, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			failedFiles = append(failedFiles, f.Name+": "+err.Error())
			continue
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			os.Remove(tempPath)
			failedFiles = append(failedFiles, f.Name+": "+err.Error())
			continue
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			os.Remove(tempPath)
			failedFiles = append(failedFiles, f.Name+": "+err.Error())
			continue
		}

		// 尝试重命名临时文件到正式文件
		if err := os.Rename(tempPath, fpath); err != nil {
			// 重命名失败，保留临时文件
			failedFiles = append(failedFiles, f.Name+": "+err.Error()+" (已保存为 "+tempPath+")")
			continue
		}

		// 设置文件权限（如果有）
		if f.Mode() != 0 {
			if err := os.Chmod(fpath, f.Mode()); err != nil {
				// 权限设置失败不影响文件本身
				// 仅记录警告，不加入失败列表
			}
		}
	}

	if len(failedFiles) > 0 {
		pterm.Warning.Printf("部分文件解压失败:\n")
		for _, fail := range failedFiles {
			fmt.Printf("  - %s\n", fail)
		}
	}

	return nil
}
