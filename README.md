# logfilewriter

logfilewriter实现了一个简单的日志文件管理功能。

## Features

- 自定义日志文件写入路径
- 自定义日志文件名
- 日志文件按固定大小自动切割
- 日志文件自动归档
- 日志归档自动压缩（gzip）
- 自定义日志归档路径
- 自定义日志保留归档天数

## 示例

```go
package main

import (
	"log"

	"github.com/eachain/logfilewriter"
)

func main() {
	w := logfilewriter.New(
		logfilewriter.WithDir("log"),
		logfilewriter.WithFileName("your_app_name.log"),
		logfilewriter.WithFileSizeLimit(100*1024*1024), // 100MB
		logfilewriter.WithArchiveDir("archive"),
		logfilewriter.WithCompress(),
		logfilewriter.WithRotateDays(7),
	)
	defer w.Close()

	log.SetOutput(w)
	log.Printf("this is a log")
}
```