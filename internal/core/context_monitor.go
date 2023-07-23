package core

import (
	"context"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"sync"
)

// TODO: 做一个context的监控组件, 用于监控回话的状态

type ctxMonitorParent interface {
	logger.Writer
}

// 用来监控http请求在服务器中的状态, 仅用于debug
type contextMonitor struct {
	parent   ctxMonitorParent
	contexts []*gin.Context
	mu       sync.Mutex
}

func newContextMonitor(parent ctxMonitorParent) *contextMonitor {
	return &contextMonitor{parent: parent}
}

func (cm *contextMonitor) run(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				cm.parent.Log(logger.Info, "closing gin context monitor...")
				return
			default:
				for _, req := range cm.contexts {
					select {
					case <-req.Done():
						cm.Log(logger.Info, "request done: "+req.Request.URL.String())

					default:
						cm.Log(logger.Info, "content length in header: "+req.GetHeader("Content-Length"))
						cm.Log(logger.Info, "writer already written")
					}
				}

			}
		}
	}()
}

func (cm *contextMonitor) addContext(ctx *gin.Context) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.contexts = append(cm.contexts, ctx)
}

func (cm *contextMonitor) Log(level logger.Level, format string, args ...interface{}) {
	cm.parent.Log(level, format, args)
}

func (cm *contextMonitor) delContext(ctx *gin.Context) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	tmp := make([]*gin.Context, 10)
	for _, item := range cm.contexts {
		if !item.IsAborted() && item == ctx {
			tmp = append(tmp, item)
		}
	}
	cm.contexts = tmp
}
