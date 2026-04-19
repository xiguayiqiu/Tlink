package cmd

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"tlink/pkg/discovery"
	"tlink/pkg/protocol"
	"tlink/pkg/session"
	"tlink/pkg/transfer"

	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var sendRemoteIP string

var sendCmd = &cobra.Command{
	Use:     "send",
	Short:   "启动监听或连接",
	Long:    `启动监听等待连接；使用 -R <端口> 监听指定端口，或使用 -R <ip:port> 连接到对方`,
	Aliases: []string{"s"},
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		uid := uuid.New().String()
		deviceName, _ := os.Hostname()

		saveDir, _ := os.Getwd()
		if receiveOutputPath != "" {
			info, err := os.Stat(receiveOutputPath)
			if err == nil && info.IsDir() {
				saveDir = receiveOutputPath
			} else if err == nil && !info.IsDir() {
				saveDir = filepath.Dir(receiveOutputPath)
			} else {
				if err := os.MkdirAll(receiveOutputPath, 0755); err == nil {
					saveDir = receiveOutputPath
				}
			}
		}

		var conn net.Conn
		var peerName string

		if sendRemoteIP != "" {
			parts := strings.SplitN(sendRemoteIP, ":", 2)
			if len(parts) == 2 && parts[0] != "" {
				var port int
				fmt.Sscanf(parts[1], "%d", &port)
				if port <= 0 || port > 65535 {
					port = 9876
				}

				pterm.Info.Printf("正在连接到 %s:%d...\n", parts[0], port)
				var err error
				conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", parts[0], port), 30*time.Second)
				if err != nil {
					pterm.Error.Printf("连接失败: %v\n", err)
					os.Exit(1)
				}

				connectData := protocol.ConnectData{
					UID:        uid,
					DeviceName: deviceName,
				}
				msg := protocol.NewMessage(protocol.MsgTypeConnect, connectData)
				if err := sendMessage(conn, msg); err != nil {
					pterm.Error.Printf("发送连接消息失败: %v\n", err)
					os.Exit(1)
				}

				ackMsg, err := readMessage(conn)
				if err != nil {
					pterm.Error.Printf("等待接受失败: %v\n", err)
					os.Exit(1)
				}
				if ackMsg.Type != protocol.MsgTypeAccept {
					pterm.Error.Printf("连接被拒绝: %s\n", ackMsg.Type)
					os.Exit(1)
				}

				peerConnectData, _ := protocol.ParseConnectData(ackMsg.Data)
				if peerConnectData != nil {
					peerName = peerConnectData.DeviceName
				} else {
					peerName = "未知设备"
				}
			} else {
				var port int
				portStr := sendRemoteIP
				if strings.HasPrefix(portStr, ":") {
					portStr = portStr[1:]
				}
				_, err := fmt.Sscanf(portStr, "%d", &port)
				if err != nil || port <= 0 || port > 65535 {
					port = 9876
				}

				listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
				if err != nil {
					pterm.Error.Printf("无法监听端口 %d: %v\n", port, err)
					os.Exit(1)
				}
				defer listener.Close()

				actualPort := listener.Addr().(*net.TCPAddr).Port
				pterm.Info.Printf("正在监听 %d 端口等待连接...\n", actualPort)
				pterm.Println()

				conn, err = listener.Accept()
				if err != nil {
					pterm.Error.Printf("接受连接失败: %v\n", err)
					os.Exit(1)
				}

				msg, err := readMessage(conn)
				if err != nil {
					pterm.Error.Printf("读取连接消息失败: %v\n", err)
					os.Exit(1)
				}
				if msg.Type != protocol.MsgTypeConnect {
					pterm.Error.Printf("收到意外消息: %s\n", msg.Type)
					os.Exit(1)
				}

				connectData, err := protocol.ParseConnectData(msg.Data)
				if err != nil {
					pterm.Error.Printf("解析连接消息失败: %v\n", err)
					os.Exit(1)
				}
				peerName = connectData.DeviceName

				pterm.Warning.Println("收到连接请求：")
				pterm.Info.Printf("来自：%s (UID: %s)\n", connectData.DeviceName, connectData.UID)
				prompt, _ := pterm.DefaultInteractiveTextInput.
					WithDefaultText("是否接受？(y/n): ").
					Show()

				input := strings.TrimSpace(strings.ToLower(prompt))

				if input != "y" && input != "yes" {
					pterm.Warning.Println("已拒绝连接")
					return
				}

				ackData := protocol.ConnectData{
					UID:        uid,
					DeviceName: deviceName,
				}
				ackMsg := protocol.NewMessage(protocol.MsgTypeAccept, ackData)
				if err := sendMessage(conn, ackMsg); err != nil {
					pterm.Error.Printf("发送接受消息失败: %v\n", err)
					os.Exit(1)
				}
			}
		} else {
			sender := transfer.NewSender(uid, deviceName, "")
			defer sender.Close()

			var err error
			tcpPort, err := sender.Listen()
			if err != nil {
				pterm.Error.Printf("无法监听端口: %v\n", err)
				os.Exit(1)
			}

			var fileName string
			var fileSize int64
			if len(args) > 0 {
				filePath := args[0]
				fileInfo, err := os.Stat(filePath)
				if err == nil && !fileInfo.IsDir() {
					fileName = filepath.Base(filePath)
					fileSize = fileInfo.Size()
				}
			}

			ds := discovery.NewDiscoveryService(uid, deviceName, tcpPort, fileName, fileSize, true)
			if err := ds.Start(); err != nil {
				pterm.Warning.Printf("发现服务启动失败: %v\n", err)
			}
			defer ds.Stop()

			pterm.Info.Printf("等待接收方连接... (UID: %s)\n", uid)
			if fileName != "" {
				pterm.Info.Printf("文件: %s (%s)\n", fileName, protocol.FormatSize(fileSize))
			}
			pterm.Println()

			connectData, err := sender.Accept()
			if err != nil {
				pterm.Error.Printf("接受连接失败: %v\n", err)
				os.Exit(1)
			}

			pterm.Warning.Println("收到连接请求：")
			pterm.Info.Printf("来自：%s (UID: %s)\n", connectData.DeviceName, connectData.UID)
			prompt, _ := pterm.DefaultInteractiveTextInput.
				WithDefaultText("是否接受？(y/n): ").
				Show()

			input := strings.TrimSpace(strings.ToLower(prompt))

			if input != "y" && input != "yes" {
				pterm.Warning.Println("已拒绝连接")
				return
			}

			if err := sender.AcceptConnection(); err != nil {
				pterm.Error.Printf("发送接受消息失败: %v\n", err)
				os.Exit(1)
			}

			pterm.Warning.Println("局域网模式下暂不支持交互式会话")
			return
		}

		sess := session.NewSession(uid, deviceName, saveDir, conn, peerName)
		sess.Start()
	},
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

func init() {
	sendCmd.Flags().StringVarP(&sendRemoteIP, "remote", "R", "", "指定监听端口（例如 9876）或连接地址（例如 192.168.1.100:9876）")
	rootCmd.AddCommand(sendCmd)
}
