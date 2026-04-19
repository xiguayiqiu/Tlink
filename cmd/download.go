package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"tlink/pkg/download"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var outputPath string
var threads int
var continueDownload bool
var proxy string

var downloadCmd = &cobra.Command{
	Use:     "download <url>",
	Short:   "下载文件",
	Long:    "从指定 URL 下载文件，支持多线程和断点续传",
	Aliases: []string{"d"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]
		downloader := download.NewDownloader(url, outputPath, threads, continueDownload, proxy)

		progressChan := make(chan *download.Progress, 100)
		done := make(chan struct {
			hash string
			err  error
		}, 1)

		pterm.Info.Println("正在准备下载...")

		var totalSize int64
		var totalChunks int
		var lastProgress *download.Progress
		var totalPercent int

		multi := pterm.DefaultMultiPrinter
		var totalWriter io.Writer
		var chunkWriters []io.Writer

		go func() {
			fileHash, err := downloader.Download(progressChan)
			done <- struct {
				hash string
				err  error
			}{fileHash, err}
		}()

		for {
			select {
			case p := <-progressChan:
				lastProgress = p
				if totalSize == 0 && p.Total > 0 {
					totalSize = p.Total
					totalChunks = p.ActiveThreads
					pterm.Info.Printf("文件大小: %s | 线程数: %d\n", download.FormatSize(totalSize), totalChunks)

					totalWriter = multi.NewWriter()
					chunkWriters = make([]io.Writer, totalChunks)

					for i := 0; i < totalChunks; i++ {
						chunkWriters[i] = multi.NewWriter()
					}

					multi.Start()
				}

				if lastProgress != nil && totalSize > 0 {
					currentTotalPercent := int(lastProgress.Percent)
					if currentTotalPercent != totalPercent || currentTotalPercent == 100 {
						speedText := download.FormatSpeed(lastProgress.Speed)
						title := fmt.Sprintf("总进度 | %s", speedText)
						bar := renderProgressBar(currentTotalPercent, 100, pterm.FgGreen, pterm.FgGray)
						fmt.Fprintf(totalWriter, "\r%s %s %3d%%", title, bar, currentTotalPercent)
						totalPercent = currentTotalPercent
					}

					if totalChunks > 1 && len(lastProgress.ChunkProgress) > 0 {
						for i := 0; i < totalChunks && i < len(chunkWriters); i++ {
							progress := lastProgress.ChunkProgress[i]
							chunkSize := totalSize / int64(totalChunks)
							if i == totalChunks-1 {
								chunkSize = totalSize - (int64(i) * chunkSize)
							}

							chunkPercent := 0
							if chunkSize > 0 {
								chunkPercent = int(float64(progress) / float64(chunkSize) * 100)
							}

							bar := renderProgressBar(chunkPercent, 100, pterm.FgCyan, pterm.FgGray)
							fmt.Fprintf(chunkWriters[i], "\r线程 %d %s %3d%%", i, bar, chunkPercent)
						}
					}
				}
			case result := <-done:
				if totalPercent < 100 {
					speedText := "完成"
					if lastProgress != nil {
						speedText = download.FormatSpeed(lastProgress.Speed)
					}
					bar := renderProgressBar(100, 100, pterm.FgGreen, pterm.FgGray)
					fmt.Fprintf(totalWriter, "\r总进度 | %s %s 100%%\n", speedText, bar)
				}

				for i := range chunkWriters {
					bar := renderProgressBar(100, 100, pterm.FgCyan, pterm.FgGray)
					fmt.Fprintf(chunkWriters[i], "\r线程 %d %s 100%%\n", i, bar)
				}

				multi.Stop()

				if result.err != nil {
					pterm.Error.Printf("下载失败: %v\n", result.err)
					os.Exit(1)
				}

				pterm.Success.Println("下载完成!")

				if lastProgress != nil {
					pterm.Info.Printf(
						"下载速度: %s | 耗时: %s | 大小: %s\n",
						download.FormatSpeed(lastProgress.Speed),
						download.FormatDuration(lastProgress.Elapsed),
						download.FormatSize(lastProgress.Total),
					)
				}

				if result.hash != "" {
					pterm.Success.Printf("✓ 文件完整性验证: SHA256=%s\n", result.hash)
				}

				return
			}
		}
	},
}

func renderProgressBar(percent, total int, barColor, fillerColor pterm.Color) string {
	const barWidth = 50
	progress := int(float64(percent) / float64(total) * barWidth)
	if progress > barWidth {
		progress = barWidth
	}
	if progress < 0 {
		progress = 0
	}

	bar := strings.Repeat("━", progress)
	filler := strings.Repeat("━", barWidth-progress)

	return pterm.NewStyle(barColor).Sprint(bar) + pterm.NewStyle(fillerColor).Sprint(filler)
}

func init() {
	downloadCmd.Flags().StringVarP(&outputPath, "output", "o", "", "指定保存路径")
	downloadCmd.Flags().IntVarP(&threads, "threads", "t", 0, "指定下载线程数")
	downloadCmd.Flags().BoolVarP(&continueDownload, "continue", "c", false, "启用断点续传")
	downloadCmd.Flags().StringVarP(&proxy, "proxy", "e", "", "指定代理地址 (例如: http://127.0.0.1:8080 或 socks5://127.0.0.1:1080 或 127.0.0.1:8080)")
	rootCmd.AddCommand(downloadCmd)
}
