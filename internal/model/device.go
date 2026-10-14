package model

import "time"

// Device 是一台接入了控制面的设备(某个人的笔记本、台式机……)。
//
// 每台设备持有一枚自己的 token;丢了一台只吊销那一台,不用换掉所有设备共用的
// 东西。token 本身不在这里:库里只存它的 SHA-256,明文只在发放那一刻出现一次。
type Device struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastIP     string     `json:"last_ip"`
	// Current 表示"发这次请求的就是这台设备",只在列表响应里有意义。
	Current bool `json:"current"`
}
