package util

import (
	"path"

	"golang.org/x/sys/unix"
)

func CheckPermsRW(checkPath string, uid, gid int) error {
	var stat unix.Stat_t
	err := unix.Stat(checkPath, &stat)
	if err != nil {
		return err
	}

	isOwner := stat.Uid == uint32(uid)
	isGroupMember := (stat.Gid == uint32(gid)) && !isOwner
	isOther := !isOwner && !isGroupMember

	// check requested perms
	allowsOwner := (stat.Mode&unix.S_IRUSR != 0) && (stat.Mode&unix.S_IWUSR != 0)
	allowsGroup := (stat.Mode&unix.S_IRGRP != 0) && (stat.Mode&unix.S_IWGRP != 0)
	allowsOther := (stat.Mode&unix.S_IROTH != 0) && (stat.Mode&unix.S_IWOTH != 0)

	switch {
	case isOwner && !allowsOwner:
		return unix.EACCES
	case isGroupMember && !allowsGroup:
		return unix.EACCES
	case isOther && !allowsOther:
		return unix.EACCES
	}

	// walk up the directory tree
	dir := path.Dir(checkPath)
	for dir != "/" {
		err = unix.Stat(dir, &stat)
		if err != nil {
			return err
		}

		isOwner = stat.Uid == uint32(uid)
		isGroupMember = (stat.Gid == uint32(gid)) && !isOwner
		isOther = !isOwner && !isGroupMember

		// require execute permission
		allowsOwner := stat.Mode&unix.S_IXUSR != 0
		allowsGroup := stat.Mode&unix.S_IXGRP != 0
		allowsOther := stat.Mode&unix.S_IXOTH != 0

		switch {
		case isOwner && !allowsOwner:
			return unix.EACCES
		case isGroupMember && !allowsGroup:
			return unix.EACCES
		case isOther && !allowsOther:
			return unix.EACCES
		}

		dir = path.Dir(dir)
	}

	return nil
}

func CheckPermsRX(checkPath string, uid, gid int) error {
	var stat unix.Stat_t
	err := unix.Stat(checkPath, &stat)
	if err != nil {
		return err
	}

	isOwner := stat.Uid == uint32(uid)
	isGroupMember := (stat.Gid == uint32(gid)) && !isOwner
	isOther := !isOwner && !isGroupMember

	// check requested perms
	allowsOwner := (stat.Mode&unix.S_IRUSR != 0) && (stat.Mode&unix.S_IXUSR != 0)
	allowsGroup := (stat.Mode&unix.S_IRGRP != 0) && (stat.Mode&unix.S_IXGRP != 0)
	allowsOther := (stat.Mode&unix.S_IROTH != 0) && (stat.Mode&unix.S_IXOTH != 0)

	switch {
	case isOwner && !allowsOwner:
		return unix.EACCES
	case isGroupMember && !allowsGroup:
		return unix.EACCES
	case isOther && !allowsOther:
		return unix.EACCES
	}

	// walk up the directory tree
	dir := path.Dir(checkPath)
	for dir != "/" {
		err = unix.Stat(dir, &stat)
		if err != nil {
			return err
		}

		isOwner = stat.Uid == uint32(uid)
		isGroupMember = (stat.Gid == uint32(gid)) && !isOwner
		isOther = !isOwner && !isGroupMember

		// require execute permission
		allowsOwner := stat.Mode&unix.S_IXUSR != 0
		allowsGroup := stat.Mode&unix.S_IXGRP != 0
		allowsOther := stat.Mode&unix.S_IXOTH != 0

		switch {
		case isOwner && !allowsOwner:
			return unix.EACCES
		case isGroupMember && !allowsGroup:
			return unix.EACCES
		case isOther && !allowsOther:
			return unix.EACCES
		}

		dir = path.Dir(dir)
	}

	return nil
}

func CheckPermsWX(checkPath string, uid, gid int) error {
	var stat unix.Stat_t
	err := unix.Stat(checkPath, &stat)
	if err != nil {
		return err
	}

	isOwner := stat.Uid == uint32(uid)
	isGroupMember := (stat.Gid == uint32(gid)) && !isOwner
	isOther := !isOwner && !isGroupMember

	// check requested perms
	allowsOwner := (stat.Mode&unix.S_IWUSR != 0) && (stat.Mode&unix.S_IXUSR != 0)
	allowsGroup := (stat.Mode&unix.S_IWGRP != 0) && (stat.Mode&unix.S_IXGRP != 0)
	allowsOther := (stat.Mode&unix.S_IWOTH != 0) && (stat.Mode&unix.S_IXOTH != 0)

	switch {
	case isOwner && !allowsOwner:
		return unix.EACCES
	case isGroupMember && !allowsGroup:
		return unix.EACCES
	case isOther && !allowsOther:
		return unix.EACCES
	}

	// walk up the directory tree
	dir := path.Dir(checkPath)
	for dir != "/" {
		err = unix.Stat(dir, &stat)
		if err != nil {
			return err
		}

		isOwner = stat.Uid == uint32(uid)
		isGroupMember = (stat.Gid == uint32(gid)) && !isOwner
		isOther = !isOwner && !isGroupMember

		// require execute permission
		allowsOwner := stat.Mode&unix.S_IXUSR != 0
		allowsGroup := stat.Mode&unix.S_IXGRP != 0
		allowsOther := stat.Mode&unix.S_IXOTH != 0

		switch {
		case isOwner && !allowsOwner:
			return unix.EACCES
		case isGroupMember && !allowsGroup:
			return unix.EACCES
		case isOther && !allowsOther:
			return unix.EACCES
		}

		dir = path.Dir(dir)
	}

	return nil
}
