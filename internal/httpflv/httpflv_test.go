// Copyright 2020, Chef.  All rights reserved.
// https://github.com/q191201771/lal
//
// Use of this source code is governed by a MIT-style license
// that can be found in the License file.
//
// Author: Chef (191201771@qq.com)

package httpflv_test

import (
	"log"
	"os"
	"testing"

	"github.com/bluenviron/mediamtx/internal/httpflv"
)

func TestReadFlv(t *testing.T) {
	const flvFile = "./source_flv_video.flv"
	if _, err := os.Lstat(flvFile); err != nil {
		log.Printf("WARN lstat %s error. err=%+v\n", flvFile, err)
		return
	}

	var (
		//headers []byte
		tagCount int
		//allHeader []byte
		//allRaw    []byte
	)
	// TODO: 测试一下读flv文件, 打印其中的tag的有关信息.
	tags, err := httpflv.ReadAllTagsFromFlvFile("../../test_files/source_flv_video.flv")
	if err != nil {
		log.Println("ERR file not exist")
	}

	tagCount = 1
	for _, tag := range tags {
		hdr := tag.Header
		log.Println("Reading tag indexed ", tagCount)
		log.Println("Header content:\n\tType: ", hdr.Type, "\n\tDataSize: ", hdr.DataSize, "\n\tTimestamp: ", hdr.Timestamp, "\n\tStreamId: ", hdr.StreamId)
		tagCount++
	}

	// 原来的代码...
	//ffp := httpflv.NewFlvFilePump()
	//err := ffp.Pump(flvFile, func(tag httpflv.Tag) bool {
	//	tagCount++
	//	allRaw = append(allRaw, tag.Raw...)
	//	h := fmt.Sprintf("%+v", tag.Header)
	//	allHeader = append(allHeader, []byte(h)...)
	//	return true
	//})
}
