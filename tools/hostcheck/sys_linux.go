package hostcheck

import "syscall"

func credential(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
}
