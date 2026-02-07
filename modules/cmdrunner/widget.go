package cmdrunner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime" // 新增：用于判断操作系统
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/rivo/tview"
	"github.com/wtfutil/wtf/logger"
	"github.com/wtfutil/wtf/view"
)

// Widget contains the data for this widget
type Widget struct {
	view.TextWidget

	settings *Settings

	m          sync.Mutex
	buffer     *bytes.Buffer
	runChan    chan bool
	redrawChan chan bool
}

// NewWidget creates a new instance of the widget
func NewWidget(tviewApp *tview.Application, redrawChan chan bool, settings *Settings) *Widget {
	widget := Widget{
		TextWidget: view.NewTextWidget(tviewApp, redrawChan, nil, settings.Common),

		settings: settings,
		buffer:   &bytes.Buffer{},
	}

	widget.View.SetWrap(true)
	widget.View.SetScrollable(true)

	widget.runChan = make(chan bool)
	widget.redrawChan = make(chan bool)
	go runCommandLoop(&widget)
	go redrawLoop(&widget)
	widget.runChan <- true

	return &widget
}

// Refresh signals the runCommandLoop to continue, or triggers a re-draw if the
// command is still running.
func (widget *Widget) Refresh() {
	select {
	case widget.runChan <- true:
	default:
		widget.redrawChan <- true
	}
}

// String returns the string representation of the widget
func (widget *Widget) String() string {
	args := strings.Join(widget.settings.args, " ")

	if args != "" {
		return fmt.Sprintf("%s %s", widget.settings.cmd, args)
	}

	return widget.settings.cmd
}

func (widget *Widget) Write(p []byte) (n int, err error) {
	widget.m.Lock()
	defer widget.m.Unlock()

	n, err = widget.buffer.Write(p)

	lines := widget.countLines()
	if lines > widget.settings.maxLines {
		err = widget.drainLines(lines - widget.settings.maxLines)
	}

	return n, err
}

/* -------------------- Unexported Functions -------------------- */

func (widget *Widget) countLines() int {
	return bytes.Count(widget.buffer.Bytes(), []byte{'\n'})
}

func (widget *Widget) drainLines(n int) error {
	for i := 0; i < n; i++ {
		_, err := widget.buffer.ReadBytes('\n')
		if err != nil {
			return err
		}
	}

	return nil
}

func (widget *Widget) environment() []string {
	envs := os.Environ()
	envs = append(
		envs,
		fmt.Sprintf("WTF_WIDGET_WIDTH=%d", widget.settings.width),
		fmt.Sprintf("WTF_WIDGET_HEIGHT=%d", widget.settings.height),
	)
	return envs
}

func runCommandLoop(widget *Widget) {
	for {
		<-widget.runChan
		widget.resetBuffer()
		cmd := exec.Command(widget.settings.cmd, widget.settings.args...)
		cmd.Env = widget.environment()
		cmd.Dir = widget.settings.workingDir
		var err error
		if widget.settings.pty {
			err = runCommandPty(widget, cmd)
		} else {
			err = runCommand(widget, cmd)
		}
		if err != nil {
			widget.handleError(err)
		}
		widget.redrawChan <- true
	}
}

func runCommand(widget *Widget, cmd *exec.Cmd) error {
	cmd.Stdout = widget
	return cmd.Run()
}

func runCommandPty(widget *Widget, cmd *exec.Cmd) error {
	f, err := pty.Start(cmd)
	if err != nil {
		if widget.settings.ptySuppressErrors {
			return cmd.Wait()
		} else {
			return err
		}
	}

	defer func() { _ = f.Close() }()

	// --- 修改开始：处理 Windows 兼容性 ---
	if runtime.GOOS != "windows" {
		// 在非 Windows 系统（Unix/Linux/macOS）下，SIGWINCH 常量是存在的
		// 使用 type assertion 或硬编码常量来规避 Windows 编译器的类型检查
		const sigWinch = syscall.Signal(0x1c) 

		ch := make(chan os.Signal, 1)
		signal.Notify(ch, sigWinch)
		go func() {
			for range ch {
				if err := pty.InheritSize(os.Stdin, f); err != nil {
					logger.Log(fmt.Sprintf("error resizing pty: %s", err))
				}
			}
		}()
		// 发送初始大小调整信号
		select {
		case ch <- sigWinch:
		default:
		}
		defer func() { signal.Stop(ch); close(ch) }()
	}
	// --- 修改结束 ---

	_, err = io.Copy(widget.buffer, f)
	if err != nil {
		if widget.settings.ptySuppressErrors && errors.Is(err, syscall.EIO) {
			return cmd.Wait()
		}
		return err
	}
	return cmd.Wait()
}

func (widget *Widget) handleError(err error) {
	widget.m.Lock()
	defer widget.m.Unlock()
	_, writeErr := widget.buffer.WriteString(err.Error())
	if writeErr != nil {
		return
	}
}

func redrawLoop(widget *Widget) {
	for {
		widget.Redraw(widget.content)
		if widget.settings.tail {
			widget.View.ScrollToEnd()
		}
		<-widget.redrawChan
	}
}

func (widget *Widget) content() (string, string, bool) {
	widget.m.Lock()
	result := widget.buffer.String()
	widget.m.Unlock()

	ansiTitle := tview.TranslateANSI(tview.Escape(widget.CommonSettings().Title))
	if ansiTitle == defaultTitle {
		ansiTitle = tview.TranslateANSI(tview.Escape(widget.String()))
	}
	ansiResult := tview.TranslateANSI(tview.Escape(result))

	return ansiTitle, ansiResult, false
}

func (widget *Widget) resetBuffer() {
	widget.m.Lock()
	defer widget.m.Unlock()

	widget.buffer.Reset()
}
