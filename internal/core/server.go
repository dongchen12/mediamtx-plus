package core

import (
	"net"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"
)

type Server struct {
	pathManager *pathManager
}

type streams struct {
	Publishers []stream `json:"publishers"`
	Players    []stream `json:"players"`
}

func NewServer(pm *pathManager) *Server {
	return &Server{
		pathManager: pm,
	}
}

func (server *Server) Serve(l net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		server.handleConn(w, r)
	})
	if err := http.Serve(l, mux); err != nil {
		return err
	}
	return nil
}

func (server *Server) handleConn(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("http flv handleConn panic: ", r)
		}
	}()

	url := r.URL.String()
	u := r.URL.Path
	if pos := strings.LastIndex(u, "."); pos < 0 || u[pos:] != ".flv" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	path := strings.TrimSuffix(strings.TrimLeft(u, "/"), ".flv")
	paths := strings.SplitN(path, "/", 2)
	log.Debug("url:", u, "path:", path, "paths:", paths)

	if len(paths) != 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 判断视屏流是否发布,如果没有发布,直接返回404
	//msgs := server.getStreams(w, r)
	//if msgs == nil || len(msgs.Publishers) == 0 {
	//	http.Error(w, "invalid path", http.StatusNotFound)
	//	return
	//} else {
	//	include := false
	//	for _, item := range msgs.Publishers {
	//		if item.Key == path {
	//			include = true
	//			break
	//		}
	//	}
	//	if include == false {
	//		http.Error(w, "invalid path", http.StatusNotFound)
	//		return
	//	}
	//}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	writer := NewFLVWriter(paths[0], paths[1], url, w)

	//server.handler.HandleWriter(writer)
	writer.Wait()
}
