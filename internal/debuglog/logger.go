package debuglog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const retainDays = 5

type Logger struct {
	mu      sync.Mutex
	dir     string
	path    string
	file    *os.File
	logger  *log.Logger
	verbose bool
}

func New(verbose bool) (*Logger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	workspaceDir := filepath.Join(home, ".miko", "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}
	dir := filepath.Join(workspaceDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	if err := cleanupOldLogs(dir, time.Now(), retainDays); err != nil {
		return nil, fmt.Errorf("cleanup old logs: %w", err)
	}

	filename := fmt.Sprintf("miko-%s.log", time.Now().Format("2006-01-02"))
	path := filepath.Join(dir, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &Logger{
		dir:     dir,
		path:    path,
		file:    file,
		logger:  log.New(file, "", 0),
		verbose: verbose,
	}, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) Directory() string {
	return l.dir
}

func (l *Logger) Path() string {
	return l.path
}

func (l *Logger) Info(component string, message string) {
	l.write("INFO", component, "", message)
}

func (l *Logger) Infof(component string, format string, args ...any) {
	l.write("INFO", component, "", fmt.Sprintf(format, args...))
}

func (l *Logger) Debug(component string, message string) {
	if !l.verbose {
		return
	}
	l.write("DEBUG", component, "", message)
}

func (l *Logger) Debugf(component string, format string, args ...any) {
	if !l.verbose {
		return
	}
	l.write("DEBUG", component, "", fmt.Sprintf(format, args...))
}

func (l *Logger) Warnf(component string, format string, args ...any) {
	l.write("WARN", component, "", fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(component string, format string, args ...any) {
	l.write("ERROR", component, "", fmt.Sprintf(format, args...))
}

func (l *Logger) SessionInfo(sessionID string, component string, message string) {
	l.write("INFO", component, sessionID, message)
}

func (l *Logger) SessionInfof(sessionID string, component string, format string, args ...any) {
	l.write("INFO", component, sessionID, fmt.Sprintf(format, args...))
}

func (l *Logger) SessionDebugf(sessionID string, component string, format string, args ...any) {
	if !l.verbose {
		return
	}
	l.write("DEBUG", component, sessionID, fmt.Sprintf(format, args...))
}

func (l *Logger) SessionWarnf(sessionID string, component string, format string, args ...any) {
	l.write("WARN", component, sessionID, fmt.Sprintf(format, args...))
}

func (l *Logger) SessionErrorf(sessionID string, component string, format string, args ...any) {
	l.write("ERROR", component, sessionID, fmt.Sprintf(format, args...))
}

func (l *Logger) write(level string, component string, sessionID string, message string) {
	if l == nil || l.logger == nil {
		return
	}
	message = strings.ReplaceAll(strings.TrimSpace(message), "\n", " ")
	if message == "" {
		return
	}

	now := time.Now().Format(time.RFC3339)
	line := fmt.Sprintf("%s [%s] [%s]", now, level, component)
	if sessionID != "" {
		line += fmt.Sprintf(" [session:%s]", sessionID)
	}
	line += " " + message

	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Println(line)
}

func cleanupOldLogs(dir string, now time.Time, keepDays int) error {
	if keepDays <= 0 {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type datedLog struct {
		name string
		date time.Time
	}

	var logs []datedLog
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := parseLogDate(entry.Name())
		if !ok {
			continue
		}
		logs = append(logs, datedLog{
			name: entry.Name(),
			date: date,
		})
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].date.Before(logs[j].date)
	})

	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(keepDays - 1))
	for _, item := range logs {
		if item.date.Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, item.name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func parseLogDate(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "miko-") || !strings.HasSuffix(name, ".log") {
		return time.Time{}, false
	}
	datePart := strings.TrimSuffix(strings.TrimPrefix(name, "miko-"), ".log")
	parsed, err := time.Parse("2006-01-02", datePart)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
