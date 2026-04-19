package download

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsgo/cmdutil"
	"golang.org/x/net/proxy"
)

// 下载块状态
type chunkState struct {
	Index   int   `json:"index"`
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	Written int64 `json:"written"`
}

// 下载进度状态
type downloadState struct {
	URL        string       `json:"url"`
	TotalSize  int64        `json:"total_size"`
	ChunkCount int          `json:"chunk_count"`
	Chunks     []chunkState `json:"chunks"`
	LastUpdate time.Time    `json:"last_update"`
}

type Downloader struct {
	url              string
	outputPath       string
	threads          int
	continueDownload bool
	proxy            string
	maxRetries       int // 最大重试次数
}

type Progress struct {
	Total         int64
	Downloaded    int64
	Speed         float64
	Percent       float64
	Remaining     time.Duration
	Elapsed       time.Duration
	ActiveThreads int
	ChunkProgress map[int]int64
}

func NewDownloader(url, outputPath string, threads int, continueDownload bool, proxy string) *Downloader {
	return &Downloader{
		url:              url,
		outputPath:       outputPath,
		threads:          threads,
		continueDownload: continueDownload,
		proxy:            proxy,
		maxRetries:       5,
	}
}

func (d *Downloader) Download(progressChan chan<- *Progress) (string, error) {
	// 创建 http client
	client := &http.Client{
		Timeout: 0,
	}

	// 如果设置了代理，添加代理
	if d.proxy != "" {
		var transport *http.Transport

		// 检查是否有协议前缀
		if strings.HasPrefix(d.proxy, "http://") || strings.HasPrefix(d.proxy, "https://") {
			// HTTP/HTTPS 代理
			proxyURL, err := url.Parse(d.proxy)
			if err == nil && proxyURL != nil {
				transport = &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
				}
			}
		} else if strings.HasPrefix(d.proxy, "socks5://") || strings.HasPrefix(d.proxy, "socks5h://") {
			// SOCKS5 代理
			proxyAddr := strings.TrimPrefix(strings.TrimPrefix(d.proxy, "socks5h://"), "socks5://")
			dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err == nil {
				transport = &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return dialer.Dial(network, addr)
					},
				}
			}
		} else {
			// 只提供了 ip:port，尝试 SOCKS5 和 HTTP
			// 首先尝试 SOCKS5
			dialer, err := proxy.SOCKS5("tcp", d.proxy, nil, proxy.Direct)
			if err == nil {
				transport = &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return dialer.Dial(network, addr)
					},
				}
			} else {
				// SOCKS5 失败，尝试 HTTP
				httpProxyURL, httpErr := url.Parse("http://" + d.proxy)
				if httpErr == nil && httpProxyURL != nil {
					transport = &http.Transport{
						Proxy: http.ProxyURL(httpProxyURL),
					}
				}
			}
		}

		if transport != nil {
			client.Transport = transport
		}
	}

	// 1. 获取文件信息
	req, err := http.NewRequest("HEAD", d.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl/8.19.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	totalSize := resp.ContentLength
	acceptRanges := resp.Header.Get("Accept-Ranges") == "bytes"
	fileName := d.getFileName(resp, d.url)
	savePath := d.getSavePath(fileName)
	statePath := savePath + ".download"

	// 2. 打开或创建文件
	file, err := d.openOrCreateFile(savePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// 3. 确定线程数
	threads := d.threads
	if threads <= 0 {
		threads = 4
	}

	// 4. 加载或初始化下载状态
	var state *downloadState
	var existingSize int64 = 0

	if d.continueDownload {
		state, err = d.loadDownloadState(statePath)
		if err == nil && state.TotalSize == totalSize && len(state.Chunks) > 0 {
			// 检查文件大小匹配
			stat, err := file.Stat()
			if err == nil && stat.Size() == totalSize {
				file.Seek(0, 0)
				os.Remove(statePath)
				return computeFileHash(file)
			}
			existingSize = 0
			for _, c := range state.Chunks {
				existingSize += c.Written
			}
		} else {
			// 初始化新状态
			state = d.createDownloadState(d.url, totalSize, threads)
			d.saveDownloadState(statePath, state) // 立即保存新状态
		}
	} else {
		// 不续传，初始化新状态
		state = d.createDownloadState(d.url, totalSize, threads)
		file.Truncate(0)
		d.saveDownloadState(statePath, state) // 立即保存新状态
	}

	// 5. 发送初始进度
	if progressChan != nil {
		chunkProgress := make(map[int]int64)
		for _, c := range state.Chunks {
			chunkProgress[c.Index] = c.Written
		}
		progressChan <- &Progress{
			Total:         totalSize,
			Downloaded:    existingSize,
			Speed:         0,
			Percent:       float64(existingSize) / float64(totalSize) * 100,
			Remaining:     0,
			Elapsed:       0,
			ActiveThreads: threads,
			ChunkProgress: chunkProgress,
		}
	}

	// 6. 开始下载
	var resultHash string
	if acceptRanges && threads > 1 {
		resultHash, err = d.downloadMultiWithResume(client, file, state, statePath, progressChan)
	} else if acceptRanges {
		resultHash, err = d.downloadSingleWithResume(client, file, state, statePath, progressChan)
	} else {
		resultHash, err = d.downloadSingleWithResume(client, file, state, statePath, progressChan)
	}

	// 7. 清理临时状态文件
	if err == nil {
		os.Remove(statePath)
	}

	return resultHash, err
}

// 创建下载状态
func (d *Downloader) createDownloadState(url string, totalSize int64, threads int) *downloadState {
	state := &downloadState{
		URL:        url,
		TotalSize:  totalSize,
		ChunkCount: threads,
		Chunks:     make([]chunkState, threads),
		LastUpdate: time.Now(),
	}

	chunkSize := totalSize / int64(threads)
	for i := 0; i < threads; i++ {
		state.Chunks[i].Index = i
		state.Chunks[i].Start = int64(i) * chunkSize
		if i == threads-1 {
			state.Chunks[i].End = totalSize - 1
		} else {
			state.Chunks[i].End = int64(i+1)*chunkSize - 1
		}
		state.Chunks[i].Written = 0
	}

	return state
}

// 加载下载状态
func (d *Downloader) loadDownloadState(statePath string) (*downloadState, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}

	var state downloadState
	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// 保存下载状态
func (d *Downloader) saveDownloadState(statePath string, state *downloadState) error {
	state.LastUpdate = time.Now()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0644)
}

func (d *Downloader) openOrCreateFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.MkdirAll(dir, 0755)
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
}

func (d *Downloader) downloadSingle(client *http.Client, file *os.File, totalSize int64, progressChan chan<- *Progress) (string, error) {
	req, err := http.NewRequest("GET", d.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl/8.19.0")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: 状态码 %d", resp.StatusCode)
	}

	buf := make([]byte, 128*1024)
	downloaded := int64(0)
	startTime := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return "", err
			}
			downloaded += int64(n)

			if progressChan != nil {
				elapsed := time.Since(startTime)
				speed := float64(downloaded) / elapsed.Seconds()
				percent := float64(0)
				remaining := time.Duration(0)

				if totalSize > 0 {
					percent = float64(downloaded) / float64(totalSize) * 100
					if speed > 0 {
						remaining = time.Duration((totalSize-downloaded)/int64(speed)) * time.Second
					}
				}

				progressChan <- &Progress{
					Total:         totalSize,
					Downloaded:    downloaded,
					Speed:         speed,
					Percent:       percent,
					Remaining:     remaining,
					Elapsed:       elapsed,
					ActiveThreads: 1,
					ChunkProgress: map[int]int64{0: downloaded},
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if totalSize > 0 && fileInfo.Size() != totalSize {
		return "", fmt.Errorf("下载不完整: 期望 %d 字节，实际 %d 字节", totalSize, fileInfo.Size())
	}

	file.Seek(0, 0)
	fileHash, err := computeFileHash(file)
	return fileHash, err
}

func (d *Downloader) downloadSingleWithResume(client *http.Client, file *os.File, state *downloadState, statePath string, progressChan chan<- *Progress) (string, error) {
	existingSize := int64(0)
	for _, c := range state.Chunks {
		existingSize += c.Written
	}

	req, err := http.NewRequest("GET", d.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl/8.19.0")
	req.Header.Set("Accept", "*/*")
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: 状态码 %d", resp.StatusCode)
	}

	file.Seek(existingSize, 0)
	buf := make([]byte, 128*1024)
	downloaded := existingSize
	startTime := time.Now()
	lastSaveTime := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return "", err
			}
			downloaded += int64(n)

			// 更新状态
			if len(state.Chunks) > 0 {
				state.Chunks[0].Written = downloaded
			}

			// 更频繁地保存状态
			if time.Since(lastSaveTime) > 500*time.Millisecond {
				d.saveDownloadState(statePath, state)
				lastSaveTime = time.Now()
			}

			if progressChan != nil {
				elapsed := time.Since(startTime)
				speed := float64(downloaded-existingSize) / elapsed.Seconds()
				percent := float64(0)
				remaining := time.Duration(0)

				if state.TotalSize > 0 {
					percent = float64(downloaded) / float64(state.TotalSize) * 100
					if speed > 0 {
						remaining = time.Duration((state.TotalSize-downloaded)/int64(speed)) * time.Second
					}
				}

				chunkProgress := make(map[int]int64)
				for _, c := range state.Chunks {
					chunkProgress[c.Index] = c.Written
				}

				progressChan <- &Progress{
					Total:         state.TotalSize,
					Downloaded:    downloaded,
					Speed:         speed,
					Percent:       percent,
					Remaining:     remaining,
					Elapsed:       elapsed,
					ActiveThreads: len(state.Chunks),
					ChunkProgress: chunkProgress,
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			d.saveDownloadState(statePath, state)
			return "", err
		}
	}

	// 保存最终状态
	d.saveDownloadState(statePath, state)

	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if state.TotalSize > 0 && fileInfo.Size() != state.TotalSize {
		return "", fmt.Errorf("下载不完整: 期望 %d 字节，实际 %d 字节", state.TotalSize, fileInfo.Size())
	}

	file.Seek(0, 0)
	fileHash, err := computeFileHash(file)
	return fileHash, err
}

func (d *Downloader) downloadMultiWithResume(client *http.Client, file *os.File, state *downloadState, statePath string, progressChan chan<- *Progress) (string, error) {
	wg := &cmdutil.WorkerGroup{Max: len(state.Chunks)}
	var mu sync.Mutex
	errChan := make(chan error, len(state.Chunks))

	startTime := time.Now()
	lastSaveTime := time.Now()

	for i := range state.Chunks {
		c := &state.Chunks[i]
		wg.Run(func() {
			chunkSize := c.End - c.Start + 1
			if c.Written >= chunkSize {
				// 这个块已经下载完成
				return
			}

			currentPos := c.Start + c.Written
			retries := 0

			for retries < d.maxRetries {
				req, err := http.NewRequest("GET", d.url, nil)
				if err != nil {
					errChan <- err
					return
				}
				req.Header.Set("User-Agent", "curl/8.19.0")
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", currentPos, c.End))

				resp, err := client.Do(req)
				if err != nil {
					retries++
					time.Sleep(time.Duration(retries) * time.Second)
					continue
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusPartialContent {
					resp.Body.Close()
					retries++
					time.Sleep(time.Duration(retries) * time.Second)
					continue
				}

				buf := make([]byte, 128*1024)
				offset := currentPos
				success := true

				for {
					n, err := resp.Body.Read(buf)
					if n > 0 {
						mu.Lock()
						if _, err := file.WriteAt(buf[:n], offset); err != nil {
							mu.Unlock()
							success = false
							break
						}
						offset += int64(n)
						c.Written = offset - c.Start

						// 更频繁地保存状态
						if time.Since(lastSaveTime) > 500*time.Millisecond {
							d.saveDownloadState(statePath, state)
							lastSaveTime = time.Now()
						}

						if progressChan != nil {
							elapsed := time.Since(startTime)
							totalDownloaded := int64(0)
							for _, chunk := range state.Chunks {
								totalDownloaded += chunk.Written
							}

							speed := float64(totalDownloaded) / elapsed.Seconds()
							percent := float64(totalDownloaded) / float64(state.TotalSize) * 100
							remaining := time.Duration(0)
							if speed > 0 {
								remaining = time.Duration((state.TotalSize-totalDownloaded)/int64(speed)) * time.Second
							}

							chunkProgress := make(map[int]int64)
							for _, chunk := range state.Chunks {
								chunkProgress[chunk.Index] = chunk.Written
							}

							progressChan <- &Progress{
								Total:         state.TotalSize,
								Downloaded:    totalDownloaded,
								Speed:         speed,
								Percent:       percent,
								Remaining:     remaining,
								Elapsed:       elapsed,
								ActiveThreads: len(state.Chunks),
								ChunkProgress: chunkProgress,
							}
						}
						mu.Unlock()
					}
					if err == io.EOF {
						break
					}
					if err != nil {
						success = false
						break
					}
				}

				if success {
					break
				}

				retries++
				time.Sleep(time.Duration(retries) * time.Second)
			}

			if retries >= d.maxRetries {
				errChan <- fmt.Errorf("块 %d 下载失败: 达到最大重试次数", c.Index)
			}
		})
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			d.saveDownloadState(statePath, state)
			return "", err
		}
	}

	d.saveDownloadState(statePath, state)

	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if fileInfo.Size() != state.TotalSize {
		return "", fmt.Errorf("下载不完整: 期望 %d 字节，实际 %d 字节", state.TotalSize, fileInfo.Size())
	}

	file.Seek(0, 0)
	fileHash, err := computeFileHash(file)
	return fileHash, err
}

func (d *Downloader) getFileName(resp *http.Response, url string) string {
	if d.outputPath != "" {
		return filepath.Base(d.outputPath)
	}

	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(cd, "filename="); idx != -1 {
			fileName := cd[idx+9:]
			fileName = strings.Trim(fileName, "\"'")
			return fileName
		}
	}

	return filepath.Base(url)
}

func (d *Downloader) getSavePath(fileName string) string {
	if d.outputPath != "" {
		info, err := os.Stat(d.outputPath)
		if err == nil && info.IsDir() {
			return filepath.Join(d.outputPath, fileName)
		}
		return d.outputPath
	}

	wd, _ := os.Getwd()
	return filepath.Join(wd, fileName)
}

func FormatSpeed(bytesPerSec float64) string {
	if bytesPerSec < 1024 {
		return fmt.Sprintf("%.2f B/s", bytesPerSec)
	} else if bytesPerSec < 1024*1024 {
		return fmt.Sprintf("%.2f KB/s", bytesPerSec/1024)
	} else if bytesPerSec < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MB/s", bytesPerSec/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB/s", bytesPerSec/(1024*1024*1024))
}

func FormatDuration(d time.Duration) string {
	if d < 100*time.Millisecond {
		return "< 1秒"
	}
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

func computeFileHash(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
