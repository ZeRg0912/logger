// Package logger provides flexible logger functionality with
// configurable output destinations, log levels, and file rotation.
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	fileWriter   io.Writer
	maxFileSize  int64
	basePath     string
	currentSize  int64
	mu           sync.Mutex
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
		return err
	}
	l.currentSize = info.Size()
	l.fileWriter = file

	return nil
}

// log is the internal method that handles actual log message processing and output.
func (l *Logger) log(level LogLevel, levelStr string, format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, v...)
	timestamp := log.Ldate | log.Ltime

	// Write to console
	if l.outputMode == ConsoleOnly || l.outputMode == Both {
		if level >= l.consoleLevel {
			consoleLogger := log.New(getConsoleWriter(level), levelStr+": ", timestamp)
			consoleLogger.Println(msg)
		}
	}

	// Write to file
	if (l.outputMode == FileOnly || l.outputMode == Both) && l.fileWriter != nil {
		if level >= l.fileLevel {
			// Check rotation
			if l.shouldRotate() {
				l.rotateFile()
			}

			fileLogger := log.New(l.fileWriter, "", 0)
			fileLogger.Printf("%s %s: %s - %s\n", time.Now().Format("2006/01/02 15:04:04"), levelStr, sourceInfo, msg)
		}
	}
}

// shouldRotate checks if log file rotation is needed based on file size.
func (l *Logger) shouldRotate() bool {
	return l.maxFileSize > 0 && l.currentSize >= l.maxFileSize
}

// rotateFile performs log file rotation by renaming existing files and creating a new one.
func (l *Logger) rotateFile() error {
	if l.fileWriter == nil {
		return nil
	}

	if file, ok := l.fileWriter.(*os.File); ok {
		file.Close()
	}

	ext := filepath.Ext(l.basePath)
	baseName := l.basePath[:len(l.basePath)-len(ext)]

	for i := 4; i >= 0; i-- {
		var oldPath string
		if i == 0 {
			oldPath = l.basePath
		} else {
			oldPath = fmt.Sprintf("%s_%d%s", baseName, i, ext)
		}

		newPath := fmt.Sprintf("%s_%d%s", baseName, i+1, ext)

		if _, err := os.Stat(oldPath); err == nil {
			os.Rename(oldPath, newPath)
		}
	}

	return l.createFileWriter()
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
		fmt.Fprintln(os.Stderr, "❌ Error:", msg)
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
		fmt.Println("ℹ️ ", msg)
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
		fmt.Println("✅", msg)
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
