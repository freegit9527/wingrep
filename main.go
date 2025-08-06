// main.go
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

func main() {
	// 命令行参数解析
	noRecursive := flag.Bool("no-recursive", false, "不递归搜索子目录")
	ignoreCase := flag.Bool("i", false, "忽略大小写")
	showLineNum := flag.Bool("n", false, "显示行号")
	hideFilename := flag.Bool("h", false, "隐藏文件名（多文件搜索时）")
	include := flag.String("include", "", "包含文件模式（如：*.go）")
	exclude := flag.String("exclude", "", "排除文件模式（如：*.log）")
	maxChars := flag.Int("max-chars", 200, "每行显示的最大字符数")
	contextChars := flag.Int("context", 20, "关键词前后保留的上下文字符数")
	textOnly := flag.Bool("text-only", true, "ture:只处理文本文件，false:处理所有文件")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: %s [选项] 模式 [路径...]\n\n选项:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n默认行为: 递归搜索子目录，只处理文本文件\n")
		fmt.Fprintf(os.Stderr, "\n示例:\n  %s 'error' src/\n  %s -n --include=*.go 'func main'\n", os.Args[0], os.Args[0])
	}

	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	pattern := args[0]
	paths := args[1:]
	if len(paths) == 0 {
		paths = []string{"."}
	}

	// 准备正则表达式
	pattern = preparePattern(pattern, *ignoreCase)
	re, err := regexp.Compile(pattern)
	if err != nil {
		log.Fatalf("无效的正则表达式: %v", err)
	}

	// 准备包含/排除过滤器
	incFilter := prepareFilter(*include)
	excFilter := prepareFilter(*exclude)

	// 递归标志 - 默认开启递归
	recursive := !*noRecursive

	// 收集要搜索的文件
	files := collectFiles(paths, recursive, incFilter, excFilter)
	if len(files) == 0 {
		fmt.Println("未找到匹配的文件")
		os.Exit(1)
	}

	// 搜索文件
	multiFile := len(files) > 1
	showFilename := multiFile && !*hideFilename
	for _, file := range files {
		searchFile(file, re, showFilename, *showLineNum, *maxChars, *contextChars, *textOnly)
	}
}

func preparePattern(pattern string, ignoreCase bool) string {
	if ignoreCase {
		return "(?i)" + pattern
	}
	return pattern
}

func prepareFilter(pattern string) func(string) bool {
	if pattern == "" {
		return nil
	}

	// 将通配符转换为正则表达式
	pattern = strings.ReplaceAll(pattern, ".", "\\.")
	pattern = strings.ReplaceAll(pattern, "*", ".*")
	pattern = strings.ReplaceAll(pattern, "?", ".")
	pattern = "^" + pattern + "$"

	re, err := regexp.Compile(pattern)
	if err != nil {
		log.Fatalf("无效的文件模式: %v", err)
	}

	return func(name string) bool {
		return re.MatchString(name)
	}
}

func collectFiles(paths []string, recursive bool, incFilter, excFilter func(string) bool) []string {
	var files []string

	for _, path := range paths {
		fileInfo, err := os.Stat(path)
		if err != nil {
			log.Printf("无法访问路径 %s: %v", path, err)
			continue
		}

		if fileInfo.IsDir() {
			walkFn := func(currentPath string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					return nil
				}
				if !includeFile(info.Name(), incFilter, excFilter) {
					return nil
				}
				files = append(files, currentPath)
				return nil
			}

			if recursive {
				filepath.Walk(path, walkFn)
			} else {
				// 非递归模式，只处理当前目录
				items, err := os.ReadDir(path)
				if err != nil {
					log.Printf("读取目录 %s 失败: %v", path, err)
					continue
				}
				for _, item := range items {
					if item.IsDir() {
						continue
					}
					fileInfo, err := item.Info()
					if err != nil {
						log.Printf("获取文件信息失败: %s: %v", item.Name(), err)
						continue
					}
					if !includeFile(fileInfo.Name(), incFilter, excFilter) {
						continue
					}
					files = append(files, filepath.Join(path, fileInfo.Name()))
				}
			}
		} else {
			// 文件路径直接处理
			if includeFile(fileInfo.Name(), incFilter, excFilter) {
				files = append(files, path)
			}
		}
	}
	return files
}

func includeFile(name string, incFilter, excFilter func(string) bool) bool {
	if excFilter != nil && excFilter(name) {
		return false
	}
	if incFilter != nil && !incFilter(name) {
		return false
	}
	return true
}

func searchFile(path string, re *regexp.Regexp, showFilename, showLineNum bool, maxChars, contextChars int, textOnly bool) {
	file, err := os.Open(path)
	if err != nil {
		log.Printf("无法打开文件 %s: %v", path, err)
		return
	}
	defer file.Close()

	// 检查是否为文本文件
	if textOnly && !isTextFile(file) {
		return
	}
	file.Seek(0, 0) // 重置文件指针

	reader := bufio.NewReaderSize(file, 1024 * 1024) // 1MB缓冲区
	lineNum := 0
	var lineBuffer bytes.Buffer

	for {
		lineNum++
		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			if err != io.EOF {
				log.Printf("读取文件 %s 错误: %v", path, err)
			}
			break
		}

		lineBuffer.Write(line)
		if !isPrefix {
			fullLine := lineBuffer.String()
			lineBuffer.Reset()

			matches := re.FindAllStringIndex(fullLine, -1)
			if matches != nil {
				for _, match := range matches {
					start, end := match[0], match[1]
					trimmedLine := extractMatchContext(fullLine, start, end, maxChars, contextChars)
					printMatch(path, trimmedLine, lineNum, showFilename, showLineNum)
				}
			}
		}
	}
}

// 检查文件是否为文本文件
func isTextFile(file *os.File) bool {
	buffer := make([]byte, 1024)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return false
	}

	// 检查前1024字节中非文本字符的比例
	nonTextCount := 0
	for i := 0; i < n; i++ {
		if buffer[i] == 0 || !utf8.RuneStart(buffer[i]) || buffer[i] < 32 && buffer[i] != '\t' && buffer[i] != '\n' && buffer[i] != '\r' {
			nonTextCount++
		}
	}

	// 如果超过10%的字符是非文本字符，则认为是二进制文件
	return float64(nonTextCount)/float64(n) < 0.1
}

// 从匹配关键词周围提取关键上下文
func extractMatchContext(line string, start, end, maxChars, contextChars int) string {
	// 计算关键词位置
	keyword := line[start:end]
	
	// 计算上下文范围
	contextStart := start - contextChars
	if contextStart < 0 {
		contextStart = 0
	}
	
	contextEnd := end + contextChars
	if contextEnd > len(line) {
		contextEnd = len(line)
	}
	
	// 添加省略号指示符
	prefix := ""
	if contextStart > 0 {
		prefix = "…"
	}
	
	suffix := ""
	if contextEnd < len(line) {
		suffix = "…"
	}
	
	// 提取上下文内容
	context := prefix + line[contextStart:contextEnd] + suffix
	
	// 计算关键词在上下文中的位置
	keywordStart := start - contextStart
	if contextStart > 0 {
		keywordStart += len(prefix) // 考虑前缀省略号长度
	}
	
	keywordEnd := keywordStart + (end - start)
	
	// 如果上下文超过最大长度，进一步精简
	if len(context) > maxChars {
		// 确保关键词可见
		availableSpace := maxChars - len(keyword)
		
		// 在关键词前后各保留部分上下文
		before := availableSpace / 2
		after := availableSpace - before
		
		trimStart := keywordStart - before
		if trimStart < 0 {
			trimStart = 0
		}
		
		trimEnd := keywordEnd + after
		if trimEnd > len(context) {
			trimEnd = len(context)
		}
		
		trimmed := context[trimStart:trimEnd]
		
		// 添加起始和结束指示符
		if trimStart > 0 {
			trimmed = "…" + trimmed
		}
		
		if trimEnd < len(context) {
			trimmed += "…"
		}
		
		return trimmed
	}
	
	return context
}

// 精简输出行，突出显示匹配部分
func printMatch(path, line string, lineNum int, showFilename, showLineNum bool) {
	if showFilename {
		fmt.Printf("%s:", path)
	}
	if showLineNum {
		fmt.Printf("%d:", lineNum)
	}
	fmt.Println(line)
}