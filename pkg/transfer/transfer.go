package transfer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
	"tlink/pkg/protocol"
)

const (
	CHUNK_SIZE       = 64 * 1024
	TCP_CONN_TIMEOUT = 30 * time.Second
	TRANSFER_TIMEOUT = 60 * time.Second
)

type Sender struct {
	uid        string
	deviceName string
	filePath   string
	listener   net.Listener
	conn       net.Conn
}

type Receiver struct {
	uid        string
	deviceName string
	listener   net.Listener
	conn       net.Conn
}

func NewSender(uid, deviceName, filePath string) *Sender {
	return &Sender{
		uid:        uid,
		deviceName: deviceName,
		filePath:   filePath,
	}
}

func (s *Sender) Listen(ports ...int) (int, error) {
	var err error
	var addr string
	if len(ports) > 0 && ports[0] > 0 {
		addr = fmt.Sprintf(":%d", ports[0])
	} else {
		addr = ":0"
	}
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	return s.listener.Addr().(*net.TCPAddr).Port, nil
}

func (s *Sender) Accept() (*protocol.ConnectData, error) {
	var err error
	s.conn, err = s.listener.Accept()
	if err != nil {
		return nil, err
	}

	msg, err := readMessage(s.conn)
	if err != nil {
		return nil, err
	}

	if msg.Type != protocol.MsgTypeConnect {
		return nil, fmt.Errorf("unexpected message type: %s", msg.Type)
	}

	return protocol.ParseConnectData(msg.Data)
}

func (s *Sender) Connect(address string, port int) error {
	var err error
	s.conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", address, port), TCP_CONN_TIMEOUT)
	if err != nil {
		return err
	}
	return nil
}

func (s *Sender) AcceptConnection() error {
	msg := protocol.NewMessage(protocol.MsgTypeAccept, nil)
	return sendMessage(s.conn, msg)
}

func (s *Sender) SendConnect() error {
	connectData := protocol.ConnectData{
		UID:        s.uid,
		DeviceName: s.deviceName,
	}
	msg := protocol.NewMessage(protocol.MsgTypeConnect, connectData)
	return sendMessage(s.conn, msg)
}

func (s *Sender) WaitForAccept() error {
	msg, err := readMessage(s.conn)
	if err != nil {
		return err
	}
	if msg.Type != protocol.MsgTypeAccept {
		return fmt.Errorf("connection rejected or unexpected message")
	}
	return nil
}

func (s *Sender) SendFile(progressChan chan<- int64) error {
	fileInfo, err := os.Stat(s.filePath)
	if err != nil {
		return err
	}

	file, err := os.Open(s.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	metadata := protocol.FileMetadataData{
		FileName:     filepath.Base(s.filePath),
		FileSize:     fileInfo.Size(),
		LastModified: fileInfo.ModTime().Unix(),
	}

	fileHash, err := ComputeFileHash(file)
	if err == nil {
		metadata.FileHash = fileHash
	}
	file.Seek(0, 0)

	msg := protocol.NewMessage(protocol.MsgTypeFileMetadata, metadata)
	if err := sendMessage(s.conn, msg); err != nil {
		return err
	}

	ackMsg, err := readMessage(s.conn)
	if err != nil {
		return err
	}
	if ackMsg.Type != protocol.MsgTypeMetadataAck {
		return fmt.Errorf("unexpected message: %s", ackMsg.Type)
	}

	buffer := make([]byte, CHUNK_SIZE)
	chunkIndex := 0
	totalSent := int64(0)

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}

		chunkData := protocol.FileChunkData{
			ChunkIndex: chunkIndex,
			Data:       buffer[:n],
		}

		chunkHash := ComputeHash(buffer[:n])
		chunkData.ChunkHash = chunkHash

		chunkMsg := protocol.NewMessage(protocol.MsgTypeFileChunk, chunkData)
		if err := sendMessage(s.conn, chunkMsg); err != nil {
			return err
		}

		totalSent += int64(n)
		if progressChan != nil {
			progressChan <- totalSent
		}

		chunkIndex++
	}

	completeMsg := protocol.NewMessage(protocol.MsgTypeTransferComplete, nil)
	return sendMessage(s.conn, completeMsg)
}

func (s *Sender) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
	if s.listener != nil {
		s.listener.Close()
	}
}

func NewReceiver(uid, deviceName string) *Receiver {
	return &Receiver{
		uid:        uid,
		deviceName: deviceName,
	}
}

func (r *Receiver) Listen(ports ...int) (int, error) {
	var err error
	var addr string
	if len(ports) > 0 && ports[0] > 0 {
		addr = fmt.Sprintf(":%d", ports[0])
	} else {
		addr = ":0"
	}
	r.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	return r.listener.Addr().(*net.TCPAddr).Port, nil
}

func (r *Receiver) Accept() (*protocol.ConnectData, error) {
	var err error
	r.conn, err = r.listener.Accept()
	if err != nil {
		return nil, err
	}

	msg, err := readMessage(r.conn)
	if err != nil {
		return nil, err
	}

	if msg.Type != protocol.MsgTypeConnect {
		return nil, fmt.Errorf("unexpected message type: %s", msg.Type)
	}

	return protocol.ParseConnectData(msg.Data)
}

func (r *Receiver) SendAccept() error {
	msg := protocol.NewMessage(protocol.MsgTypeAccept, nil)
	return sendMessage(r.conn, msg)
}

func (r *Receiver) Connect(address string, port int) error {
	var err error
	r.conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", address, port), TCP_CONN_TIMEOUT)
	if err != nil {
		return err
	}

	connectData := protocol.ConnectData{
		UID:        r.uid,
		DeviceName: r.deviceName,
	}
	msg := protocol.NewMessage(protocol.MsgTypeConnect, connectData)
	return sendMessage(r.conn, msg)
}

func (r *Receiver) WaitForAccept() error {
	msg, err := readMessage(r.conn)
	if err != nil {
		return err
	}
	if msg.Type != protocol.MsgTypeAccept {
		return fmt.Errorf("connection rejected or unexpected message")
	}
	return nil
}

func (r *Receiver) ReceiveMetadata() (*protocol.FileMetadataData, error) {
	metadataMsg, err := readMessage(r.conn)
	if err != nil {
		return nil, err
	}
	if metadataMsg.Type != protocol.MsgTypeFileMetadata {
		return nil, fmt.Errorf("unexpected message type: %s", metadataMsg.Type)
	}

	metadata, err := protocol.ParseFileMetadataData(metadataMsg.Data)
	if err != nil {
		return nil, err
	}

	ackMsg := protocol.NewMessage(protocol.MsgTypeMetadataAck, nil)
	if err := sendMessage(r.conn, ackMsg); err != nil {
		return nil, err
	}

	return metadata, nil
}

func (r *Receiver) ReceiveFileData(metadata *protocol.FileMetadataData, saveDir string, progressChan chan<- int64) error {
	savePath := filepath.Join(saveDir, metadata.FileName)
	file, err := os.Create(savePath)
	if err != nil {
		return err
	}
	defer file.Close()

	totalReceived := int64(0)
	expectedChunks := (metadata.FileSize + CHUNK_SIZE - 1) / CHUNK_SIZE
	receivedChunks := 0

	for receivedChunks < int(expectedChunks) {
		chunkMsg, err := readMessage(r.conn)
		if err != nil {
			return err
		}

		if chunkMsg.Type == protocol.MsgTypeTransferComplete {
			break
		}

		if chunkMsg.Type != protocol.MsgTypeFileChunk {
			return fmt.Errorf("unexpected message: %s", chunkMsg.Type)
		}

		chunkData, err := protocol.ParseFileChunkData(chunkMsg.Data)
		if err != nil {
			return err
		}

		if chunkData.ChunkHash != "" {
			computedHash := ComputeHash(chunkData.Data)
			if computedHash != chunkData.ChunkHash {
				return fmt.Errorf("chunk %d hash mismatch: expected %s, got %s", chunkData.ChunkIndex, chunkData.ChunkHash, computedHash)
			}
		}

		if _, err := file.Write(chunkData.Data); err != nil {
			return err
		}

		totalReceived += int64(len(chunkData.Data))
		if progressChan != nil {
			progressChan <- totalReceived
		}

		receivedChunks++
	}

	file.Seek(0, 0)
	if metadata.FileHash != "" {
		computedFileHash, err := ComputeFileHash(file)
		if err != nil {
			return err
		}
		if computedFileHash != metadata.FileHash {
			return fmt.Errorf("file hash mismatch: expected %s, got %s", metadata.FileHash, computedFileHash)
		}
	}

	return nil
}

func (r *Receiver) ReceiveFile(saveDir string, progressChan chan<- int64) (*protocol.FileMetadataData, error) {
	metadata, err := r.ReceiveMetadata()
	if err != nil {
		return nil, err
	}

	if err := r.ReceiveFileData(metadata, saveDir, progressChan); err != nil {
		return nil, err
	}

	return metadata, nil
}

func (r *Receiver) Close() {
	if r.conn != nil {
		r.conn.Close()
	}
	if r.listener != nil {
		r.listener.Close()
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

func ComputeHash(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

func ComputeFileHash(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
