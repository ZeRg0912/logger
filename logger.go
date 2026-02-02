// Package logger provides flexible logger functionality with
// configurable output destinations, log levels, and file rotation.
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// LogLevel represents the severity level of log messages.
type LogLevel int

const (
	LevelDebug LogLevel = iota // Debug level for detailed development information
	LevelInfo                  // Info level for general operational messages
	LevelWarn                  // Warn level for warning conditions
	LevelError                 // Error level for error conditions
)

// OutputMode defines where log messages should be written.
type OutputMode int

const (
	ConsoleOnly OutputMode = iota // Log only to console
	FileOnly                      // Log only to file
	Both                          // Log to both console and file
)

// Logger is the main logger structure that manages log configuration and output.
type Logger struct {
	consoleLevel LogLevel
	fileLevel    LogLevel
	outputMode   OutputMode

	fileWriter  io.Writer
	maxFileSize int64

	// baePath is the "template" path from config, e.g. logs/app.log
	// Actual log files are created with timestamp suffix based on basePath.
	basePath string

	// filePath is the currently opened actual file path with timestamp suffix.
	filePath string

	currentSize int64
	mu          sync.Mutex
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// Init initializes the logger with the specified configuration.
// outputMode determines where logs are written (console, file, or both).
// consoleLevel sets the minimum log level for console output.
// fileLevel sets the minimum log level for file output.
// filePath specifies the log file path (required for file output modes).
// maxFileSize sets the maximum log file size in bytes before rotation (0 disables rotation).
// Returns an error if file initialization fails.
func Init(outputMode OutputMode, consoleLevel, fileLevel LogLevel, filePath string, maxFileSize int64) error {
	var err error
	once.Do(func() {
		defaultLogger, err = newLogger(outputMode, consoleLevel, fileLevel, filePath, maxFileSize)
	})
	return err
}

// InitConsoleOnly initializes a logger that writes only to console.
// consoleLevel sets the minimum log level for console output.
func InitConsoleOnly(consoleLevel LogLevel) error {
	return Init(ConsoleOnly, consoleLevel, LevelDebug, "", 0)
}

// InitFileOnly initializes a logger that writes only to file.
// fileLevel sets the minimum log level for file output.
// filePath specifies the log file path.
// maxFileSize sets the maximum log file size in bytes before rotation.
func InitFileOnly(fileLevel LogLevel, filePath string, maxFileSize int64) error {
	return Init(FileOnly, LevelDebug, fileLevel, filePath, maxFileSize)
}

// InitBoth initializes a logger that writes to both console and file.
// consoleLevel sets the minimum log level for console output.
// fileLevel sets the minimum log level for file output.
// filePath specifies the log file path.
// maxFileSize sets the maximum log file size in bytes before rotation.
func InitBoth(consoleLevel, fileLevel LogLevel, filePath string, maxFileSize int64) error {
	return Init(Both, consoleLevel, fileLevel, filePath, maxFileSize)
}

// Close closes underlying file writer (if any). Safe to call multiple times.
func Close() error {
	if defaultLogger == nil {
		return nil
	}
	return defaultLogger.Close()
}

// Close closes file resources of this logger (if any). Safe to call multiple times.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if file, ok := l.fileWriter.(*os.File); ok {
		err := file.Close()
		l.fileWriter = nil
		l.currentSize = 0
		l.filePath = ""
		return err
	}
	l.fileWriter = nil
	l.currentSize = 0
	l.filePath = ""
	return nil
}

// newLogger creates a new Logger instance with the specified configuration.
func newLogger(outputMode OutputMode, consoleLevel, fileLevel LogLevel, filePath string, maxFileSize int64) (*Logger, error) {
	l := &Logger{
		outputMode:   outputMode,
		consoleLevel: consoleLevel,
		fileLevel:    fileLevel,
		basePath:     filePath,
		maxFileSize:  maxFileSize,
	}

	// Create file writer if needed
	if (outputMode == FileOnly || outputMode == Both) && filePath != "" {
		if err := l.createFileWriter(); err != nil {
			return nil, err
		}
	}

	return l, nil
}

// createFileWriter initializes the log file and directory structure.
func (l *Logger) createFileWriter() error {
	dir := filepath.Dir(l.basePath)
	if dir != "." && dir != string(filepath.Separator) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(l.basePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}

	l.currentSize = info.Size()
	l.fileWriter = file
	return nil
}

func (l *Logger) formatLine(levelStr string, sourceInfo string, msg string) string {
	return fmt.Sprintf("%s %s: %s - %s\n", time.Now().Format("2006/01/02 15:04:04"), levelStr, sourceInfo, msg)
}

func (l *Logger) writeConsole(level LogLevel, line string) {
	_, _ = io.WriteString(getConsoleWriter(level), line)
}

func (l *Logger) writeFile(line string) {
	if l.fileWriter == nil {
		_ = l.openNewFileLocked()
		if l.fileWriter == nil {
			return
		}
	}

	nextBytes := int64(len(line))
	if l.shouldRotate(nextBytes) {
		_ = l.rotateLocked()
		if l.fileWriter == nil {
			return
		}
	}

	n, err := io.WriteString(l.fileWriter, line)
	if err == nil {
		l.currentSize += int64(n)
	}
}

// log is the internal method that handles actual log message processing and output.
func (l *Logger) log(level LogLevel, levelStr string, format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, v...)
	_, file, line, _ := runtime.Caller(2)
	fileName := filepath.Base(file)
	sourceInfo := fmt.Sprintf("%s:%d", fileName, line)

	logLine := l.formatLine(levelStr, sourceInfo, msg)

	// Write to console
	if (l.outputMode == ConsoleOnly || l.outputMode == Both) && level >= l.consoleLevel {
		l.writeConsole(level, logLine)
	}

	// Write to file
	if (l.outputMode == FileOnly || l.outputMode == Both) && level >= l.fileLevel {
		l.writeFile(logLine)
	}
}

// shouldRotate checks if log file rotation is needed based on file size.
func (l *Logger) shouldRotate(nextBytes int64) bool {
	return l.maxFileSize > 0 && (l.currentSize+nextBytes) > l.maxFileSize
}

// rotateLocked closes current file and opens a new timestamp file.
// Must be called under l.mu.
// no file-count limit: old files are kept.
func (l *Logger) rotateLocked() error {
	return l.openNewFileLocked()
}

// openNewFileLocked opens a new timestamp file based on l.basePath.
// Must be called under l.mu.
func (l *Logger) openNewFileLocked() error {
	if l.basePath == "" {
		return fmt.Errorf("log file path is empty")
	}

	if err := ensureDir(l.basePath); err != nil {
		return err
	}

	path, err := uniqueLogPath(l.basePath)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	// Close old file if any
	if old, ok := l.fileWriter.(*os.File); ok && old != nil {
		_ = old.Close()
	}

	l.fileWriter = file
	l.filePath = path

	if stat, err := file.Stat(); err == nil {
		l.currentSize = stat.Size()
	} else {
		l.currentSize = 0
	}

	return nil
}

// ensureDir creates directory for file path if needed.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" || dir == string(filepath.Separator) {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// timestampSuffix returns a Windows safe timestamp with seconds.
func timestampSuffix() string {
	return time.Now().Format("02.01.2006_15-04-02")
}

// pathWithSuffix inserts suffix before extension:
// logs/app.log + "31.01.2026_23-10-15" - > logs/app_31.01.2026_23-10-15.log
func pathWithSuffix(basePath, suffix string) string {
	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath)

	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]

	newBase := fmt.Sprintf("%s_%s%s", name, suffix, ext)
	if dir == "." || dir == "" {
		return newBase
	}
	return filepath.Join(dir, newBase)
}

// uniqueLogPath picks a unique timestamped file path. If collision occurs, adds _01, _02, ...
func uniqueLogPath(basePath string) (string, error) {
	suffix := timestampSuffix()
	candidatePath := pathWithSuffix(basePath, suffix)

	_, statErr := os.Stat(candidatePath)
	if os.IsNotExist(statErr) {
		return candidatePath, nil
	}
	if statErr != nil {
		return "", statErr
	}

	for i := 1; i <= 9999; i++ {
		nextSuffix := fmt.Sprintf("%s_%02d", suffix, i)
		nextPath := pathWithSuffix(basePath, nextSuffix)

		_, statErr = os.Stat(nextPath)
		if os.IsNotExist(statErr) {
			return nextPath, nil
		}
		if statErr != nil {
			return "", statErr
		}
	}

	msSUffix := time.Now().Format("02.01.2006_15-40-05.000")
	return pathWithSuffix(basePath, msSUffix), nil
}

// getConsoleWriter returns the appropriate console writer based on log level.
// Errors are written to stderr, other levels to stdout.
func getConsoleWriter(level LogLevel) io.Writer {
	if level == LevelError {
		return os.Stderr
	}
	return os.Stdout
}

// Debug logs a debug level message with formatting.
// These messages are typically used for detailed development information.
func Debug(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelDebug, "DEBUG", format, v...)
	}
}

// Info logs an info level message with formatting.
// These messages are used for general operational information.
func Info(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelInfo, "INFO", format, v...)
	}
}

// Warn logs a warning level message with formatting.
// These messages indicate potentially harmful situations.
func Warn(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelWarn, "WARN", format, v...)
	}
}

// Error logs an error level message with formatting.
// These messages indicate error conditions that might still allow the application to continue running.
func Error(format string, v ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelError, "ERROR", format, v...)
	}
}

// ConsoleError displays an error message to the user in the console.
// Always shows in console (regardless of log level) and also logs to file if configured.
// Formats the message with emoji for better visibility.
func ConsoleError(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	// Always show error to user in console
	if defaultLogger == nil || defaultLogger.outputMode == ConsoleOnly || defaultLogger.outputMode == Both {
		fmt.Fprintln(os.Stderr, "Error:", msg)
	}

	// Log to file if needed
	if defaultLogger != nil && (defaultLogger.outputMode == FileOnly || defaultLogger.outputMode == Both) {
		defaultLogger.log(LevelError, "ERROR", format, v...)
	}
}

// ConsoleInfo displays an informational message to the user in the console.
// Always shows in console and also logs to file if configured.
// Formats the message with emoji for better visibility.
func ConsoleInfo(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	if defaultLogger == nil || defaultLogger.outputMode == ConsoleOnly || defaultLogger.outputMode == Both {
		fmt.Println("Info:", msg)
	}

	if defaultLogger != nil && (defaultLogger.outputMode == FileOnly || defaultLogger.outputMode == Both) {
		defaultLogger.log(LevelInfo, "INFO", format, v...)
	}
}

// ConsoleSuccess displays a success message to the user in the console.
// Always shows in console and also logs to file if configured.
// Formats the message with emoji for better visibility.
func ConsoleSuccess(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	if defaultLogger == nil || defaultLogger.outputMode == ConsoleOnly || defaultLogger.outputMode == Both {
		fmt.Println("Success:", msg)
	}

	if defaultLogger != nil && (defaultLogger.outputMode == FileOnly || defaultLogger.outputMode == Both) {
		defaultLogger.log(LevelInfo, "INFO", format, v...)
	}
}

// ConsoleHelp displays a help message to the user in the console.
// Only shows in console, never logs to file.
// Use for command usage information and help text.
func ConsoleHelp(message string) {
	if defaultLogger == nil || defaultLogger.outputMode == ConsoleOnly || defaultLogger.outputMode == Both {
		fmt.Println(message)
	}
}

// ConsoleHelpf displays a formatted help message to the user in the console.
// Only shows in console, never logs to file.
// Use for formatted command usage information and help text.
func ConsoleHelpf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if defaultLogger == nil || defaultLogger.outputMode == ConsoleOnly || defaultLogger.outputMode == Both {
		fmt.Println(msg)
	}
}
