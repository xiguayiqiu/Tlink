package protocol

import (
	"encoding/json"
	"fmt"
)

const (
	MsgTypeDiscovery       = "DISCOVERY"
	MsgTypeConnect         = "CONNECT"
	MsgTypeAccept          = "ACCEPT"
	MsgTypeFileMetadata    = "FILE_METADATA"
	MsgTypeMetadataAck     = "METADATA_ACK"
	MsgTypeFileChunk       = "FILE_CHUNK"
	MsgTypeTransferComplete = "TRANSFER_COMPLETE"
	MsgTypeCancelTransfer  = "CANCEL_TRANSFER"
)

type Message struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type DiscoveryData struct {
	UID         string `json:"uid"`
	DeviceName  string `json:"device_name"`
	TCPPort     int    `json:"tcp_port"`
	FileName    string `json:"file_name,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
}

type ConnectData struct {
	UID        string `json:"uid"`
	DeviceName string `json:"device_name"`
}

type FileMetadataData struct {
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	LastModified int64  `json:"last_modified"`
	FileHash     string `json:"file_hash,omitempty"`
	IsCompressed bool   `json:"is_compressed,omitempty"`
}

type FileChunkData struct {
	ChunkIndex int    `json:"chunk_index"`
	Data       []byte `json:"data"`
	ChunkHash  string `json:"chunk_hash,omitempty"`
}

func NewMessage(msgType string, data interface{}) *Message {
	return &Message{
		Type: msgType,
		Data: data,
	}
}

func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

func FromJSON(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func ParseDiscoveryData(data interface{}) (*DiscoveryData, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var dd DiscoveryData
	if err := json.Unmarshal(bytes, &dd); err != nil {
		return nil, err
	}
	return &dd, nil
}

func ParseConnectData(data interface{}) (*ConnectData, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var cd ConnectData
	if err := json.Unmarshal(bytes, &cd); err != nil {
		return nil, err
	}
	return &cd, nil
}

func ParseFileMetadataData(data interface{}) (*FileMetadataData, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var fmd FileMetadataData
	if err := json.Unmarshal(bytes, &fmd); err != nil {
		return nil, err
	}
	return &fmd, nil
}

func ParseFileChunkData(data interface{}) (*FileChunkData, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var fcd FileChunkData
	if err := json.Unmarshal(bytes, &fcd); err != nil {
		return nil, err
	}
	return &fcd, nil
}

func FormatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.2f KB", float64(size)/1024)
	} else if size < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", float64(size)/(1024*1024*1024))
}
