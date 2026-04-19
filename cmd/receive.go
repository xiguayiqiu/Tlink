package cmd

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"tlink/pkg/discovery"
	"tlink/pkg/protocol"
	"tlink/pkg/session"

	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var receiveOutputPath string
var receiveRemoteIP string

var receiveCmd = &cobra.Command{
	Use:     "receive",
	Short:   "接收文件",
	Long:    `以接收方模式运行；使用 -R <端口> 监听指定端口，或使用 -R <ip:port> 连接到对方`,
	Aliases: []string{"r"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		uid := uuid.New().String()
		deviceName, _ := os.Hostname()

		var saveDir string
		if receiveOutputPath != "" {
			info, err := os.Stat(receiveOutputPath)
			if err == nil && info.IsDir() {
				saveDir = receiveOutputPath
			} else if err == nil && !info.IsDir() {
				saveDir = filepath.Dir(receiveOutputPath)
			} else {
				if err := os.MkdirAll(receiveOutputPath, 0755); err == nil {
					saveDir = receiveOutputPath
				} else {
					saveDir, _ = os.Getwd()
				}
			}
		} else {
			saveDir, _ = os.Getwd()
		}

		var conn net.Conn
		var peerName string

		if receiveRemoteIP != "" {
			parts := strings.SplitN(receiveRemoteIP, ":", 2)
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
				portStr := receiveRemoteIP
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
			ds := discovery.NewDiscoveryService(uid, deviceName, 0, "", 0, false)
			if err := ds.Start(); err != nil {
				fmt.Printf("错误: 发现服务启动失败: %v\n", err)
				os.Exit(1)
			}
			defer ds.Stop()

			fmt.Println("正在发现发送方...")
			fmt.Println("按 Ctrl+C 退出")
			fmt.Printf("保存目录: %s\n", saveDir)
			fmt.Println()

			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			refreshChan := make(chan struct{}, 1)
			refreshChan <- struct{}{}

			go func() {
				for range ds.PeerUpdateChan() {
					select {
					case refreshChan <- struct{}{}:
					default:
					}
				}
			}()

			for {
				select {
				case <-refreshChan:
					displayPeers(ds)
				case <-ticker.C:
					displayPeers(ds)
				}

				peers := ds.GetPeers()
				if len(peers) > 0 {
					selectedPeer := promptSelectPeer(peers)
					if selectedPeer != nil {
						connectAndReceive(uid, deviceName, selectedPeer, saveDir)
						return
					}
				}
			}
		}

		sess := session.NewSession(uid, deviceName, saveDir, conn, peerName)
		sess.Start()
	},
}

func displayPeers(ds *discovery.DiscoveryService) {
	peers := ds.GetPeers()

	fmt.Print("\033[H\033[2J")
	fmt.Println("发现以下发送方：")
	fmt.Println()

	if len(peers) == 0 {
		fmt.Println("  (暂无发送方，请确保发送方已启动)")
		fmt.Println()
		return
	}

	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen)
	})

	for i, peer := range peers {
		fileInfo := ""
		if peer.FileName != "" {
			fileInfo = fmt.Sprintf(" - %s (%s)", peer.FileName, protocol.FormatSize(peer.FileSize))
		}
		fmt.Printf("  [%d] %s (UID: %s)%s\n", i+1, peer.DeviceName, peer.UID[:8]+"...", fileInfo)
	}
	fmt.Println()
}

func promptSelectPeer(peers []*discovery.PeerInfo) *discovery.PeerInfo {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("请选择要连接的发送方 (输入编号，或按 Enter 刷新): ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return nil
	}

	var index int
	_, err := fmt.Sscanf(input, "%d", &index)
	if err != nil || index < 1 || index > len(peers) {
		fmt.Println("无效的编号，请重试")
		return nil
	}

	return peers[index-1]
}

func connectAndReceive(uid, deviceName string, peer *discovery.PeerInfo, saveDir string) {
	pterm.Warning.Println("局域网模式下暂不支持交互式会话")
	return
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

func init() {
	receiveCmd.Flags().StringVarP(&receiveOutputPath, "output", "o", "", "指定接收文件的保存路径")
	receiveCmd.Flags().StringVarP(&receiveRemoteIP, "remote", "R", "", "指定监听端口（例如 9876）或连接地址（例如 192.168.1.100:9876）")
	rootCmd.AddCommand(receiveCmd)
}
