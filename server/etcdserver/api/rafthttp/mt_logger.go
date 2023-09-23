package rafthttp

import (
	"fmt"
	"log"
	"os"
)

type Logger struct {
	stdout *log.Logger
	stderr *log.Logger
}

func (logger *Logger) Debug(format string, args ...any) {
	return
	if len(args) == 0 {
		logger.stdout.Println(format)
	} else {
		logger.stdout.Printf(format+"\n", args...)
	}
}

func (logger *Logger) Info(format string, args ...any) {
	if len(args) == 0 {
		logger.stdout.Println(format)
	} else {
		logger.stdout.Printf(format+"\n", args...)
	}
}

func (logger *Logger) Error(format string, args ...any) {
	if len(args) == 0 {
		logger.stderr.Println(format)
	} else {
		logger.stderr.Printf(format+"\n", args...)
	}
}

func (logger *Logger) ErrorErr(err error, format string, args ...any) {
	if len(args) == 0 {
		logger.stderr.Println(format + "\n" + err.Error())
	} else {
		logger.stderr.Printf(format+"\n"+err.Error()+"\n", args...)
	}
}

func NewLogger(prefix string) *Logger {
	stdoutLogger := log.New(os.Stdout, fmt.Sprintf("[ INFO | %s ] ", prefix), log.Ldate|log.Ltime)
	stderrLogger := log.New(os.Stderr, fmt.Sprintf("[ ERROR | %s ] ", prefix), log.Ldate|log.Ltime)
	return &Logger{stdoutLogger, stderrLogger}
}
