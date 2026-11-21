package store

// UC-26:设备模型退役。devices 与 device_password 两张表不再创建;老库里已存在
// 的表在 migrate 时直接删掉 —— 已裁定 API 上线即断、不留兼容期,这两张表里的
// 设备 token 与设备密码随之作废,CLI 改用管理员账号登录(mpx connect)。
func (s *Store) dropLegacyDevice() error {
	for _, t := range []string{"devices", "device_password"} {
		if _, err := s.db.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
			return err
		}
	}
	return nil
}
