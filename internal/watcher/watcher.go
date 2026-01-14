package watcher

import (
	"log"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher 监听指定目录的新文件创建事件
type FileWatcher struct {
	watcher  *fsnotify.Watcher
	watchDir string
}

// NewFileWatcher 创建新的文件监听器
func NewFileWatcher(dir string) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	fw := &FileWatcher{
		watcher:  watcher,
		watchDir: dir,
	}

	err = fw.watcher.Add(dir)
	if err != nil {
		return nil, err
	}

	return fw, nil
}

// Start 启动文件监听
func (fw *FileWatcher) Start() {
	go func() {
		for {
			select {
			case event, ok := <-fw.watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					if isMP4File(event.Name) {
						log.Printf("New .mp4 file detected: %s", event.Name)
						// Trigger task for the new file
					}
				}
			case err, ok := <-fw.watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Error: %s", err)
			}
		}
	}()
}

// Close 关闭文件监听器
func (fw *FileWatcher) Close() error {
	return fw.watcher.Close()
}

func isMP4File(filename string) bool {
	return len(filename) > 4 && filename[len(filename)-4:] == ".mp4"
}
