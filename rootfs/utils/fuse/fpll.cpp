/*
  FUSE: Filesystem in Userspace
  Copyright (C) 2001-2007  Miklos Szeredi <miklos@szeredi.hu>

  This program can be distributed under the terms of the GNU GPLv2.
  See the file COPYING.
*/

/** @file
 *
 * This file system mirrors the existing file system hierarchy of the
 * system, starting at the root file system. This is implemented by
 * just "passing through" all requests to the corresponding user-space
 * libc functions. In contrast to passthrough.c and passthrough_fh.c,
 * this implementation uses the low-level API. Its performance should
 * be the least bad among the three, but many operations are not
 * implemented. In particular, it is not possible to remove files (or
 * directories) because the code necessary to defer actual removal
 * until the file is not opened anymore would make the example much
 * more complicated.
 *
 * When writeback caching is enabled (-o writeback mount option), it
 * is only possible to write to files for which the mounting user has
 * read permissions. This is because the writeback cache requires the
 * kernel to be able to issue read requests for all files (which the
 * passthrough filesystem cannot satisfy if it can't read the file in
 * the underlying filesystem).
 *
 * Compile with:
 *
 *     gcc -Wall passthrough_ll.c `pkg-config fuse3 --cflags --libs` -o passthrough_ll
 *
 * ## Source code ##
 * \include passthrough_ll.c
 */

#define FUSE_USE_VERSION 34

#include <fuse_lowlevel.h>
#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
#include <stddef.h>
#include <stdbool.h>
#include <string.h>
#include <limits.h>
#include <dirent.h>
#include <assert.h>
#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <sys/file.h>
#include <sys/xattr.h>
#include <sys/socket.h>
#include <sys/un.h>

#include <thread>
#include "passthrough_helpers.h"
#include "parallel_hashmap/phmap.h"
#include "wyhash.h"

/* We are re-using pointers to our `struct lo_inode` and `struct
   lo_dirp` elements as inodes. This means that we must be able to
   store uintptr_t values in a fuse_ino_t variable. The following
   incantation checks this condition at compile time. */
#if defined(__GNUC__) && (__GNUC__ > 4 || __GNUC__ == 4 && __GNUC_MINOR__ >= 6) && !defined __cplusplus
_Static_assert(sizeof(fuse_ino_t) >= sizeof(uintptr_t),
	       "fuse_ino_t too small to hold uintptr_t values!");
#else
struct _uintptr_to_must_hold_fuse_ino_t_dummy_struct \
	{ unsigned _uintptr_to_must_hold_fuse_ino_t:
			((sizeof(fuse_ino_t) >= sizeof(uintptr_t)) ? 1 : -1); };
#endif

#define trace_printf(...) do {} while (0)

static phmap::parallel_flat_hash_map_m<fuse_ino_t, struct lo_inode *> ino_to_ptr;
static phmap::parallel_flat_hash_map_m<fuse_ino_t, std::string> forgotten_inodes;
static phmap::parallel_flat_hash_map_m<std::string, fuse_ino_t> root_dir_inodes;
static phmap::parallel_flat_hash_map_m<fuse_ino_t, std::string> root_dir_names;
static std::pair<dev_t, ino_t> root_inode_key = {0, 0};

struct lo_inode {
	int fd;
	ino_t ino;
	dev_t dev;
	uint64_t refcount; /* protected by lo->mutex */
	fuse_ino_t nodeid;
};

enum {
	CACHE_NEVER,
	CACHE_NORMAL,
	CACHE_ALWAYS,
};

struct lo_data {
	pthread_mutex_t mutex;
	int debug;
	int writeback;
	int flock;
	int xattr;
	char *source;
	double timeout;
	int cache;
	int timeout_set;
	struct lo_inode root; /* protected by lo->mutex */
};

static const struct fuse_opt lo_opts[] = {
	{ "writeback",
	  offsetof(struct lo_data, writeback), 1 },
	{ "no_writeback",
	  offsetof(struct lo_data, writeback), 0 },
	{ "source=%s",
	  offsetof(struct lo_data, source), 0 },
	{ "flock",
	  offsetof(struct lo_data, flock), 1 },
	{ "no_flock",
	  offsetof(struct lo_data, flock), 0 },
	{ "xattr",
	  offsetof(struct lo_data, xattr), 1 },
	{ "no_xattr",
	  offsetof(struct lo_data, xattr), 0 },
	{ "timeout=%lf",
	  offsetof(struct lo_data, timeout), 0 },
	{ "timeout=",
	  offsetof(struct lo_data, timeout_set), 1 },
	{ "cache=never",
	  offsetof(struct lo_data, cache), CACHE_NEVER },
	{ "cache=auto",
	  offsetof(struct lo_data, cache), CACHE_NORMAL },
	{ "cache=always",
	  offsetof(struct lo_data, cache), CACHE_ALWAYS },

	FUSE_OPT_END
};

static struct lo_data *lo_data(fuse_req_t req)
{
	return (struct lo_data *) fuse_req_userdata(req);
}

static struct lo_inode *lo_inode(fuse_req_t req, fuse_ino_t ino)
{
	if (ino == FUSE_ROOT_ID)
		return &lo_data(req)->root;
	else
		return ino_to_ptr[ino];
}

static uint64_t hash_st_ino(dev_t dev, ino_t ino) {
	// root must always be 1
	if (dev == root_inode_key.first && ino == root_inode_key.second) {
		return FUSE_ROOT_ID;
	}

	// need a better hash than XOR. XOR causes stale file handle very quickly
	char buf[16] = {0};
	memcpy(buf, &dev, sizeof(dev_t));
	memcpy(buf + sizeof(dev_t), &ino, sizeof(ino_t));
	return wyhash(buf, sizeof(dev_t) + sizeof(ino_t), 0, _wyp);
}

static int lo_fd(fuse_req_t req, fuse_ino_t ino)
{
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		// will naturally return EBADF
		return -1;
	}

	return inode->fd;
}

static bool lo_debug(fuse_req_t req)
{
	return lo_data(req)->debug != 0;
}

static void lo_init(void *userdata,
		    struct fuse_conn_info *conn)
{
	struct lo_data *lo = (struct lo_data*) userdata;

	if(conn->capable & FUSE_CAP_EXPORT_SUPPORT)
		conn->want |= FUSE_CAP_EXPORT_SUPPORT;

	if (lo->writeback &&
	    conn->capable & FUSE_CAP_WRITEBACK_CACHE) {
		if (lo->debug)
			fuse_log(FUSE_LOG_DEBUG, "lo_init: activating writeback\n");
		conn->want |= FUSE_CAP_WRITEBACK_CACHE;
	}
	if (lo->flock && conn->capable & FUSE_CAP_FLOCK_LOCKS) {
		if (lo->debug)
			fuse_log(FUSE_LOG_DEBUG, "lo_init: activating flock locks\n");
		conn->want |= FUSE_CAP_FLOCK_LOCKS;
	}
}

static void lo_destroy(void *userdata)
{
	for (auto it = ino_to_ptr.begin(); it != ino_to_ptr.end(); ++it) {
		struct lo_inode *inode = it->second;
		free(inode);
	}
}

static void lo_getattr(fuse_req_t req, fuse_ino_t ino,
			     struct fuse_file_info *fi)
{
	int res;
	struct stat buf;
	struct lo_data *lo = lo_data(req);

	(void) fi;

	res = fstatat(lo_fd(req, ino), "", &buf, AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW);
	if (res == -1)
		return (void) fuse_reply_err(req, errno);

	fuse_reply_attr(req, &buf, lo->timeout);
}

static void lo_setattr(fuse_req_t req, fuse_ino_t ino, struct stat *attr,
		       int valid, struct fuse_file_info *fi)
{
	int saverr;
	char procname[64];
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	int ifd = inode->fd;
	int res;

	if (valid & FUSE_SET_ATTR_MODE) {
		if (fi) {
			res = fchmod(fi->fh, attr->st_mode);
		} else {
			sprintf(procname, "/proc/self/fd/%i", ifd);
			res = chmod(procname, attr->st_mode);
		}
		if (res == -1)
			goto out_err;
	}
	if (valid & (FUSE_SET_ATTR_UID | FUSE_SET_ATTR_GID)) {
		uid_t uid = (valid & FUSE_SET_ATTR_UID) ?
			attr->st_uid : (uid_t) -1;
		gid_t gid = (valid & FUSE_SET_ATTR_GID) ?
			attr->st_gid : (gid_t) -1;

		res = fchownat(ifd, "", uid, gid,
			       AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW);
		if (res == -1)
			goto out_err;
	}
	if (valid & FUSE_SET_ATTR_SIZE) {
		if (fi) {
			res = ftruncate(fi->fh, attr->st_size);
		} else {
			sprintf(procname, "/proc/self/fd/%i", ifd);
			res = truncate(procname, attr->st_size);
		}
		if (res == -1)
			goto out_err;
	}
	if (valid & (FUSE_SET_ATTR_ATIME | FUSE_SET_ATTR_MTIME)) {
		struct timespec tv[2];

		tv[0].tv_sec = 0;
		tv[1].tv_sec = 0;
		tv[0].tv_nsec = UTIME_OMIT;
		tv[1].tv_nsec = UTIME_OMIT;

		if (valid & FUSE_SET_ATTR_ATIME_NOW)
			tv[0].tv_nsec = UTIME_NOW;
		else if (valid & FUSE_SET_ATTR_ATIME)
			tv[0] = attr->st_atim;

		if (valid & FUSE_SET_ATTR_MTIME_NOW)
			tv[1].tv_nsec = UTIME_NOW;
		else if (valid & FUSE_SET_ATTR_MTIME)
			tv[1] = attr->st_mtim;

		if (fi)
			res = futimens(fi->fh, tv);
		else {
			sprintf(procname, "/proc/self/fd/%i", ifd);
			res = utimensat(AT_FDCWD, procname, tv, 0);
		}
		if (res == -1)
			goto out_err;
	}

	return lo_getattr(req, ino, fi);

out_err:
	saverr = errno;
	fuse_reply_err(req, saverr);
}

static struct lo_inode *lo_find(struct lo_data *lo, struct stat *st)
{
	pthread_mutex_lock(&lo->mutex);
	struct lo_inode *ret = ino_to_ptr[hash_st_ino(st->st_dev, st->st_ino)];
	if (ret) {
		assert(ret->refcount > 0);
		ret->refcount++;
		pthread_mutex_unlock(&lo->mutex);
		return ret;
	}

	pthread_mutex_unlock(&lo->mutex);
	return NULL;
}

static int lo_do_lookup(fuse_req_t req, fuse_ino_t parent, const char *name,
			 struct fuse_entry_param *e)
{
	int newfd;
	int res;
	int saverr;
	struct lo_data *lo = lo_data(req);
	struct lo_inode *inode;
	bool recovered = false;

	memset(e, 0, sizeof(*e));
	e->attr_timeout = lo->timeout;
	e->entry_timeout = lo->timeout;

	// parent fd:
	auto forgotten = forgotten_inodes[parent];
	if (forgotten != "") {
		// cases are '.' and '..' (or other path)
		if (!strcmp(name, ".")) {
			trace_printf("recovering [file, %s] fd %lu from %s\n", name, parent, forgotten.c_str());
			newfd = openat(AT_FDCWD, forgotten.c_str(), O_PATH | O_NOFOLLOW);
			recovered = true;
		} else {
			char new_path[PATH_MAX];
			snprintf(new_path, PATH_MAX, "%s/%s", forgotten.c_str(), name);
			trace_printf("recovering [dir, %s] fd %lu from %s\n", name, parent, new_path);
			newfd = openat(AT_FDCWD, new_path, O_PATH | O_NOFOLLOW);
		}
	} else {
		newfd = openat(lo_fd(req, parent), name, O_PATH | O_NOFOLLOW);
	}
	if (newfd == -1)
		goto out_err;

	res = fstatat(newfd, "", &e->attr, AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW);
	if (res == -1)
		goto out_err;

	inode = lo_find(lo_data(req), &e->attr);
	if (inode) {
		close(newfd);
		newfd = -1;
	} else {
		saverr = ENOMEM;
		inode = (struct lo_inode *) calloc(1, sizeof(struct lo_inode));
		if (!inode)
			goto out_err;

		inode->refcount = 1;
		inode->fd = newfd;
		inode->ino = e->attr.st_ino;
		inode->dev = e->attr.st_dev;

		pthread_mutex_lock(&lo->mutex);
		inode->nodeid = recovered ? parent : hash_st_ino(inode->dev, inode->ino);
		ino_to_ptr[inode->nodeid] = inode;
		// MUST now delete this forgotten inode entry. it's recovered and available again
		if (recovered) {
			forgotten_inodes.erase(parent);
		}
		pthread_mutex_unlock(&lo->mutex);

		if (parent == FUSE_ROOT_ID) {
			// root dir
			std::string name_str(name);
			root_dir_inodes[name_str] = inode->nodeid;
			root_dir_names[inode->nodeid] = name_str;
		}
	}
	e->ino = inode->nodeid;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "  %lli/%s -> %lli\n",
			(unsigned long long) parent, name, (unsigned long long) e->ino);

	return 0;

out_err:
	saverr = errno;
	if (newfd != -1)
		close(newfd);
	return saverr;
}

static void lo_lookup(fuse_req_t req, fuse_ino_t parent, const char *name)
{
	struct fuse_entry_param e;
	int err;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_lookup(parent=%" PRIu64 ", name=%s)\n",
			parent, name);

	err = lo_do_lookup(req, parent, name, &e);
	if (err)
		fuse_reply_err(req, err);
	else
		fuse_reply_entry(req, &e);
}

static void lo_mknod_symlink(fuse_req_t req, fuse_ino_t parent,
			     const char *name, mode_t mode, dev_t rdev,
			     const char *link)
{
	int res;
	int saverr;
	struct lo_inode *dir = lo_inode(req, parent);
	if (!dir) {
		fuse_reply_err(req, EBADF);
		return;
	}
	struct fuse_entry_param e;

	res = mknod_wrapper(dir->fd, name, link, mode, rdev);

	saverr = errno;
	if (res == -1)
		goto out;

	saverr = lo_do_lookup(req, parent, name, &e);
	if (saverr)
		goto out;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "  %lli/%s -> %lli\n",
			(unsigned long long) parent, name, (unsigned long long) e.ino);

	fuse_reply_entry(req, &e);
	return;

out:
	fuse_reply_err(req, saverr);
}

static void lo_mknod(fuse_req_t req, fuse_ino_t parent,
		     const char *name, mode_t mode, dev_t rdev)
{
	lo_mknod_symlink(req, parent, name, mode, rdev, NULL);
}

static void lo_mkdir(fuse_req_t req, fuse_ino_t parent, const char *name,
		     mode_t mode)
{
	lo_mknod_symlink(req, parent, name, S_IFDIR | mode, 0, NULL);
}

static void lo_symlink(fuse_req_t req, const char *link,
		       fuse_ino_t parent, const char *name)
{
	lo_mknod_symlink(req, parent, name, S_IFLNK, 0, link);
}

static void lo_link(fuse_req_t req, fuse_ino_t ino, fuse_ino_t parent,
		    const char *name)
{
	int res;
	struct lo_data *lo = lo_data(req);
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	struct fuse_entry_param e;
	char procname[64];
	int saverr;

	memset(&e, 0, sizeof(struct fuse_entry_param));
	e.attr_timeout = lo->timeout;
	e.entry_timeout = lo->timeout;

	sprintf(procname, "/proc/self/fd/%i", inode->fd);
	res = linkat(AT_FDCWD, procname, lo_fd(req, parent), name,
		     AT_SYMLINK_FOLLOW);
	if (res == -1)
		goto out_err;

	res = fstatat(inode->fd, "", &e.attr, AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW);
	if (res == -1)
		goto out_err;

	pthread_mutex_lock(&lo->mutex);
	inode->refcount++;
	pthread_mutex_unlock(&lo->mutex);
	e.ino = inode->nodeid;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "  %lli/%s -> %lli\n",
			(unsigned long long) parent, name,
			(unsigned long long) e.ino);

	fuse_reply_entry(req, &e);
	return;

out_err:
	saverr = errno;
	fuse_reply_err(req, saverr);
}

static void lo_rmdir(fuse_req_t req, fuse_ino_t parent, const char *name)
{
	int res;

	res = unlinkat(lo_fd(req, parent), name, AT_REMOVEDIR);

	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void lo_rename(fuse_req_t req, fuse_ino_t parent, const char *name,
		      fuse_ino_t newparent, const char *newname,
		      unsigned int flags)
{
	int res;

	if (flags) {
		fuse_reply_err(req, EINVAL);
		return;
	}

	res = renameat(lo_fd(req, parent), name,
			lo_fd(req, newparent), newname);

	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void lo_unlink(fuse_req_t req, fuse_ino_t parent, const char *name)
{
	int res;

	res = unlinkat(lo_fd(req, parent), name, 0);

	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void unref_inode(struct lo_data *lo, struct lo_inode *inode, uint64_t n)
{
	if (!inode)
		return;

	pthread_mutex_lock(&lo->mutex);
	assert(inode->refcount >= n);
	inode->refcount -= n;
	if (!inode->refcount) {
		char procname[64];

		// read and save full path
		sprintf(procname, "/proc/self/fd/%i", inode->fd);
		char buf[PATH_MAX + 1];
		int res = readlink(procname, buf, PATH_MAX);
		if (res == -1) {
			fprintf(stderr, "failed to readlink\n");
		} else {
			// add to map
			buf[res] = '\0';
			trace_printf("storing fd %lu from path %s\n", inode->nodeid, buf);
			forgotten_inodes[inode->nodeid] = std::string(buf);
		}
		auto root_dir_name = root_dir_names[inode->nodeid];
		if (root_dir_name != "") {
			trace_printf("removing root dir %s\n", root_dir_name.c_str());
			root_dir_inodes.erase(root_dir_name);
			root_dir_names.erase(inode->nodeid);
		}
		ino_to_ptr.erase(inode->nodeid);

		pthread_mutex_unlock(&lo->mutex);
		close(inode->fd);
		free(inode);

	} else {
		pthread_mutex_unlock(&lo->mutex);
	}
}

static void lo_forget_one(fuse_req_t req, fuse_ino_t ino, uint64_t nlookup)
{
	struct lo_data *lo = lo_data(req);
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}

	if (lo_debug(req)) {
		fuse_log(FUSE_LOG_DEBUG, "  forget %lli %lli -%lli\n",
			(unsigned long long) ino,
			(unsigned long long) inode->refcount,
			(unsigned long long) nlookup);
	}

	unref_inode(lo, inode, nlookup);
}

static void lo_forget(fuse_req_t req, fuse_ino_t ino, uint64_t nlookup)
{
	lo_forget_one(req, ino, nlookup);
	fuse_reply_none(req);
}

static void lo_forget_multi(fuse_req_t req, size_t count,
				struct fuse_forget_data *forgets)
{
	int i;

	for (i = 0; (size_t)i < count; i++)
		lo_forget_one(req, forgets[i].ino, forgets[i].nlookup);
	fuse_reply_none(req);
}

static void lo_readlink(fuse_req_t req, fuse_ino_t ino)
{
	char buf[PATH_MAX + 1];
	int res;

	res = readlinkat(lo_fd(req, ino), "", buf, sizeof(buf));
	if (res == -1)
		return (void) fuse_reply_err(req, errno);

	if (res == sizeof(buf))
		return (void) fuse_reply_err(req, ENAMETOOLONG);

	buf[res] = '\0';

	fuse_reply_readlink(req, buf);
}

struct lo_dirp {
	DIR *dp;
	struct dirent *entry;
	off_t offset;
};

static struct lo_dirp *lo_dirp(struct fuse_file_info *fi)
{
	return (struct lo_dirp *) (uintptr_t) fi->fh;
}

static void lo_opendir(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi)
{
	int error = ENOMEM;
	struct lo_data *lo = lo_data(req);
	struct lo_dirp *d;
	int fd;

	d = (struct lo_dirp *) calloc(1, sizeof(struct lo_dirp));
	if (d == NULL)
		goto out_err;

	fd = openat(lo_fd(req, ino), ".", O_RDONLY);
	if (fd == -1)
		goto out_errno;

	d->dp = fdopendir(fd);
	if (d->dp == NULL)
		goto out_errno;

	d->offset = 0;
	d->entry = NULL;

	fi->fh = (uintptr_t) d;
	if (lo->cache == CACHE_ALWAYS)
		fi->cache_readdir = 1;
	fuse_reply_open(req, fi);
	return;

out_errno:
	error = errno;
out_err:
	if (d) {
		if (fd != -1)
			close(fd);
		free(d);
	}
	fuse_reply_err(req, error);
}

static int is_dot_or_dotdot(const char *name)
{
	return name[0] == '.' && (name[1] == '\0' ||
				  (name[1] == '.' && name[2] == '\0'));
}

static void lo_do_readdir(fuse_req_t req, fuse_ino_t ino, size_t size,
			  off_t offset, struct fuse_file_info *fi, int plus)
{
	struct lo_dirp *d = lo_dirp(fi);
	char *buf;
	char *p;
	size_t rem = size;
	int err;

	(void) ino;

	buf = (char *) calloc(1, size);
	if (!buf) {
		err = ENOMEM;
		goto error;
	}
	p = buf;

	if (offset != d->offset) {
		seekdir(d->dp, offset);
		d->entry = NULL;
		d->offset = offset;
	}
	while (1) {
		size_t entsize;
		off_t nextoff;
		const char *name;

		if (!d->entry) {
			errno = 0;
			d->entry = readdir(d->dp);
			if (!d->entry) {
				if (errno) {  // Error
					err = errno;
					goto error;
				} else {  // End of stream
					break; 
				}
			}
		}
		nextoff = d->entry->d_off;
		name = d->entry->d_name;
		fuse_ino_t entry_ino = 0;
		if (plus) {
			struct fuse_entry_param e;
			if (is_dot_or_dotdot(name)) {
				e = {};
				e.attr.st_ino = d->entry->d_ino;
				e.attr.st_mode = static_cast<mode_t>(d->entry->d_type << 12);
			} else {
				err = lo_do_lookup(req, ino, name, &e);
				if (err)
					goto error;
				entry_ino = e.ino;
			}

			entsize = fuse_add_direntry_plus(req, p, rem, name,
							 &e, nextoff);
		} else {
			struct stat st = {
				.st_ino = d->entry->d_ino,
				.st_mode = static_cast<mode_t>(d->entry->d_type << 12),
			};
			entsize = fuse_add_direntry(req, p, rem, name,
						    &st, nextoff);
		}
		if (entsize > rem) {
			if (entry_ino != 0) 
				lo_forget_one(req, entry_ino, 1);
			break;
		}
		
		p += entsize;
		rem -= entsize;

		d->entry = NULL;
		d->offset = nextoff;
	}

    err = 0;
error:
    // If there's an error, we can only signal it if we haven't stored
    // any entries yet - otherwise we'd end up with wrong lookup
    // counts for the entries that are already in the buffer. So we
    // return what we've collected until that point.
    if (err && rem == size)
	    fuse_reply_err(req, err);
    else
	    fuse_reply_buf(req, buf, size - rem);
    free(buf);
}

static void lo_readdir(fuse_req_t req, fuse_ino_t ino, size_t size,
		       off_t offset, struct fuse_file_info *fi)
{
	lo_do_readdir(req, ino, size, offset, fi, 0);
}

static void lo_readdirplus(fuse_req_t req, fuse_ino_t ino, size_t size,
			   off_t offset, struct fuse_file_info *fi)
{
	lo_do_readdir(req, ino, size, offset, fi, 1);
}

static void lo_releasedir(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi)
{
	struct lo_dirp *d = lo_dirp(fi);
	(void) ino;
	closedir(d->dp);
	free(d);
	fuse_reply_err(req, 0);
}

static void lo_create(fuse_req_t req, fuse_ino_t parent, const char *name,
		      mode_t mode, struct fuse_file_info *fi)
{
	int fd;
	struct lo_data *lo = lo_data(req);
	struct fuse_entry_param e;
	int err;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_create(parent=%" PRIu64 ", name=%s)\n",
			parent, name);

	fd = openat(lo_fd(req, parent), name,
		    (fi->flags | O_CREAT) & ~O_NOFOLLOW, mode);
	if (fd == -1)
		return (void) fuse_reply_err(req, errno);

	fi->fh = fd;
	if (lo->cache == CACHE_NEVER)
		fi->direct_io = 1;
	else if (lo->cache == CACHE_ALWAYS)
		fi->keep_cache = 1;

	fi->parallel_direct_writes = 1;

	err = lo_do_lookup(req, parent, name, &e);
	if (err)
		fuse_reply_err(req, err);
	else
		fuse_reply_create(req, &e, fi);
}

static void lo_fsyncdir(fuse_req_t req, fuse_ino_t ino, int datasync,
			struct fuse_file_info *fi)
{
	int res;
	int fd = dirfd(lo_dirp(fi)->dp);
	(void) ino;
	if (datasync)
		res = fdatasync(fd);
	else
		res = fsync(fd);
	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void lo_open(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi)
{
	int fd;
	char buf[64];
	struct lo_data *lo = lo_data(req);

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_open(ino=%" PRIu64 ", flags=%d)\n",
			ino, fi->flags);

	/* With writeback cache, kernel may send read requests even
	   when userspace opened write-only */
	if (lo->writeback && (fi->flags & O_ACCMODE) == O_WRONLY) {
		fi->flags &= ~O_ACCMODE;
		fi->flags |= O_RDWR;
	}

	/* With writeback cache, O_APPEND is handled by the kernel.
	   This breaks atomicity (since the file may change in the
	   underlying filesystem, so that the kernel's idea of the
	   end of the file isn't accurate anymore). In this example,
	   we just accept that. A more rigorous filesystem may want
	   to return an error here */
	if (lo->writeback && (fi->flags & O_APPEND))
		fi->flags &= ~O_APPEND;

	sprintf(buf, "/proc/self/fd/%i", lo_fd(req, ino));
	fd = open(buf, fi->flags & ~O_NOFOLLOW);
	if (fd == -1)
		return (void) fuse_reply_err(req, errno);

	fi->fh = fd;
	if (lo->cache == CACHE_NEVER)
		fi->direct_io = 1;
	else if (lo->cache == CACHE_ALWAYS)
		fi->keep_cache = 1;

	fi->parallel_direct_writes = 1;

	fuse_reply_open(req, fi);
}

static void lo_release(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi)
{
	(void) ino;

	close(fi->fh);
	fuse_reply_err(req, 0);
}

static void lo_flush(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi)
{
	// omitted: we run on btrfs, where there are no special semantics for close(dup)
	fuse_reply_err(req, 0);
}

static void lo_fsync(fuse_req_t req, fuse_ino_t ino, int datasync,
		     struct fuse_file_info *fi)
{
	int res;
	(void) ino;
	if (datasync)
		res = fdatasync(fi->fh);
	else
		res = fsync(fi->fh);
	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void lo_read(fuse_req_t req, fuse_ino_t ino, size_t size,
		    off_t offset, struct fuse_file_info *fi)
{
	struct fuse_bufvec buf = FUSE_BUFVEC_INIT(size);

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_read(ino=%" PRIu64 ", size=%zd, "
			"off=%lu)\n", ino, size, (unsigned long) offset);

	buf.buf[0].flags = static_cast<fuse_buf_flags>(FUSE_BUF_IS_FD | FUSE_BUF_FD_SEEK);
	buf.buf[0].fd = fi->fh;
	buf.buf[0].pos = offset;

	fuse_reply_data(req, &buf, FUSE_BUF_SPLICE_MOVE);
}

static void lo_write_buf(fuse_req_t req, fuse_ino_t ino,
			 struct fuse_bufvec *in_buf, off_t off,
			 struct fuse_file_info *fi)
{
	(void) ino;
	ssize_t res;
	struct fuse_bufvec out_buf = FUSE_BUFVEC_INIT(fuse_buf_size(in_buf));

	out_buf.buf[0].flags = static_cast<fuse_buf_flags>(FUSE_BUF_IS_FD | FUSE_BUF_FD_SEEK);
	out_buf.buf[0].fd = fi->fh;
	out_buf.buf[0].pos = off;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_write(ino=%" PRIu64 ", size=%zd, off=%lu)\n",
			ino, out_buf.buf[0].size, (unsigned long) off);

	res = fuse_buf_copy(&out_buf, in_buf, static_cast<fuse_buf_copy_flags>(0));
	if(res < 0)
		fuse_reply_err(req, -res);
	else
		fuse_reply_write(req, (size_t) res);
}

static void lo_statfs(fuse_req_t req, fuse_ino_t ino)
{
	int res;
	struct statvfs stbuf;

	res = fstatvfs(lo_fd(req, ino), &stbuf);
	if (res == -1)
		fuse_reply_err(req, errno);
	else
		fuse_reply_statfs(req, &stbuf);
}

static void lo_fallocate(fuse_req_t req, fuse_ino_t ino, int mode,
			 off_t offset, off_t length, struct fuse_file_info *fi)
{
	int err = EOPNOTSUPP;
	(void) ino;

#ifdef HAVE_FALLOCATE
	err = fallocate(fi->fh, mode, offset, length);
	if (err < 0)
		err = errno;

#elif defined(HAVE_POSIX_FALLOCATE)
	if (mode) {
		fuse_reply_err(req, EOPNOTSUPP);
		return;
	}

	err = posix_fallocate(fi->fh, offset, length);
#endif

	fuse_reply_err(req, err);
}

static void lo_flock(fuse_req_t req, fuse_ino_t ino, struct fuse_file_info *fi,
		     int op)
{
	int res;
	(void) ino;

	res = flock(fi->fh, op);

	fuse_reply_err(req, res == -1 ? errno : 0);
}

static void lo_getxattr(fuse_req_t req, fuse_ino_t ino, const char *name,
			size_t size)
{
	char *value = NULL;
	char procname[64];
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	ssize_t ret;
	int saverr;

	saverr = ENOSYS;
	if (!lo_data(req)->xattr)
		goto out;

	if (lo_debug(req)) {
		fuse_log(FUSE_LOG_DEBUG, "lo_getxattr(ino=%" PRIu64 ", name=%s size=%zd)\n",
			ino, name, size);
	}

	sprintf(procname, "/proc/self/fd/%i", inode->fd);

	if (size) {
		value = (char *) malloc(size);
		if (!value)
			goto out_err;

		ret = getxattr(procname, name, value, size);
		if (ret == -1)
			goto out_err;
		saverr = 0;
		if (ret == 0)
			goto out;

		fuse_reply_buf(req, value, ret);
	} else {
		ret = getxattr(procname, name, NULL, 0);
		if (ret == -1)
			goto out_err;

		fuse_reply_xattr(req, ret);
	}
out_free:
	free(value);
	return;

out_err:
	saverr = errno;
out:
	fuse_reply_err(req, saverr);
	goto out_free;
}

static void lo_listxattr(fuse_req_t req, fuse_ino_t ino, size_t size)
{
	char *value = NULL;
	char procname[64];
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	ssize_t ret;
	int saverr;

	saverr = ENOSYS;
	if (!lo_data(req)->xattr)
		goto out;

	if (lo_debug(req)) {
		fuse_log(FUSE_LOG_DEBUG, "lo_listxattr(ino=%" PRIu64 ", size=%zd)\n",
			ino, size);
	}

	sprintf(procname, "/proc/self/fd/%i", inode->fd);

	if (size) {
		value = (char *) malloc(size);
		if (!value)
			goto out_err;

		ret = listxattr(procname, value, size);
		if (ret == -1)
			goto out_err;
		saverr = 0;
		if (ret == 0)
			goto out;

		fuse_reply_buf(req, value, ret);
	} else {
		ret = listxattr(procname, NULL, 0);
		if (ret == -1)
			goto out_err;

		fuse_reply_xattr(req, ret);
	}
out_free:
	free(value);
	return;

out_err:
	saverr = errno;
out:
	fuse_reply_err(req, saverr);
	goto out_free;
}

static void lo_setxattr(fuse_req_t req, fuse_ino_t ino, const char *name,
			const char *value, size_t size, int flags)
{
	char procname[64];
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	ssize_t ret;
	int saverr;

	saverr = ENOSYS;
	if (!lo_data(req)->xattr)
		goto out;

	if (lo_debug(req)) {
		fuse_log(FUSE_LOG_DEBUG, "lo_setxattr(ino=%" PRIu64 ", name=%s value=%s size=%zd)\n",
			ino, name, value, size);
	}

	sprintf(procname, "/proc/self/fd/%i", inode->fd);

	ret = setxattr(procname, name, value, size, flags);
	saverr = ret == -1 ? errno : 0;

out:
	fuse_reply_err(req, saverr);
}

static void lo_removexattr(fuse_req_t req, fuse_ino_t ino, const char *name)
{
	char procname[64];
	struct lo_inode *inode = lo_inode(req, ino);
	if (!inode) {
		fuse_reply_err(req, EBADF);
		return;
	}
	ssize_t ret;
	int saverr;

	saverr = ENOSYS;
	if (!lo_data(req)->xattr)
		goto out;

	if (lo_debug(req)) {
		fuse_log(FUSE_LOG_DEBUG, "lo_removexattr(ino=%" PRIu64 ", name=%s)\n",
			ino, name);
	}

	sprintf(procname, "/proc/self/fd/%i", inode->fd);

	ret = removexattr(procname, name);
	saverr = ret == -1 ? errno : 0;

out:
	fuse_reply_err(req, saverr);
}

#ifdef HAVE_COPY_FILE_RANGE
static void lo_copy_file_range(fuse_req_t req, fuse_ino_t ino_in, off_t off_in,
			       struct fuse_file_info *fi_in,
			       fuse_ino_t ino_out, off_t off_out,
			       struct fuse_file_info *fi_out, size_t len,
			       int flags)
{
	ssize_t res;

	if (lo_debug(req))
		fuse_log(FUSE_LOG_DEBUG, "lo_copy_file_range(ino=%" PRIu64 "/fd=%lu, "
				"off=%lu, ino=%" PRIu64 "/fd=%lu, "
				"off=%lu, size=%zd, flags=0x%x)\n",
			ino_in, fi_in->fh, off_in, ino_out, fi_out->fh, off_out,
			len, flags);

	res = copy_file_range(fi_in->fh, &off_in, fi_out->fh, &off_out, len,
			      flags);
	if (res < 0)
		fuse_reply_err(req, errno);
	else
		fuse_reply_write(req, res);
}
#endif

static void lo_lseek(fuse_req_t req, fuse_ino_t ino, off_t off, int whence,
		     struct fuse_file_info *fi)
{
	off_t res;

	(void)ino;
	res = lseek(fi->fh, off, whence);
	if (res != -1)
		fuse_reply_lseek(req, res);
	else
		fuse_reply_err(req, errno);
}

static const struct fuse_lowlevel_ops lo_oper = {
	.init		= lo_init,
	.destroy	= lo_destroy,
	.lookup		= lo_lookup,
	.forget		= lo_forget,
	.getattr	= lo_getattr,
	.setattr	= lo_setattr,
	.readlink	= lo_readlink,
	.mknod		= lo_mknod,
	.mkdir		= lo_mkdir,
	.unlink		= lo_unlink,
	.rmdir		= lo_rmdir,
	.symlink	= lo_symlink,
	.rename		= lo_rename,
	.link		= lo_link,
	.open		= lo_open,
	.read		= lo_read,
	.flush		= lo_flush,
	.release	= lo_release,
	.fsync		= lo_fsync,
	.opendir	= lo_opendir,
	.readdir	= lo_readdir,
	.releasedir	= lo_releasedir,
	.fsyncdir	= lo_fsyncdir,
	.statfs		= lo_statfs,
	.setxattr	= lo_setxattr,
	.getxattr	= lo_getxattr,
	.listxattr	= lo_listxattr,
	.removexattr	= lo_removexattr,
	.create		= lo_create,
	.write_buf      = lo_write_buf,
	.forget_multi	= lo_forget_multi,
	.flock		= lo_flock,
	.fallocate	= lo_fallocate,
	.readdirplus	= lo_readdirplus,
#ifdef HAVE_COPY_FILE_RANGE
	.copy_file_range = lo_copy_file_range,
#endif
	.lseek		= lo_lseek,
};

static int rpc_handle_delete(struct fuse_session *se, const char *path) {
	std::string child_name(path);
	auto ino = root_dir_inodes[child_name];
	if (ino == 0) {
		fprintf(stderr, "unknown child %s\n", path);
		return -ECHILD;
	}

	trace_printf("begin delete %s %lu\n", path, ino);
	int ret = fuse_lowlevel_notify_delete(se, FUSE_ROOT_ID, ino, child_name.c_str(), child_name.size());
	if (ret != 0) {
		fprintf(stderr, "failed to delete: %d\n", ret);
		return ret;
	}
	trace_printf("end delete %s %lu\n", path, ino);

	return 0;
}

static void serve_rpc_conn(struct fuse_session *se, int conn_fd) {
	char path_buf[PATH_MAX];
	while (true) {
		int len;
		int ret = read(conn_fd, &len, sizeof(len));
		if (ret == 0) {
			break;
		} else if (ret != sizeof(len)) {
			fprintf(stderr, "failed to read len\n");
			break;
		}

		if (len > PATH_MAX - 1) {
			fprintf(stderr, "len too large\n");
			break;
		}

		ret = read(conn_fd, path_buf, len);
		if (ret != len) {
			fprintf(stderr, "failed to read path\n");
			break;
		}
		path_buf[len] = '\0';

		ret = rpc_handle_delete(se, path_buf);
		if (ret != 0) {
			fprintf(stderr, "failed to handle delete\n");
		}

		// return response
		ret = write(conn_fd, &ret, sizeof(ret));
		if (ret != sizeof(ret)) {
			fprintf(stderr, "failed to write response\n");
			break;
		}
	}

	close(conn_fd);
}

static void listen_rpc(struct fuse_session *se) {
	int listen_fd = socket(AF_UNIX, SOCK_STREAM|SOCK_CLOEXEC, 0);
	if (listen_fd == -1) {
		fprintf(stderr, "failed to create socket\n");
		return;
	}

	struct sockaddr_un addr = { .sun_family = AF_UNIX };
	strcpy(addr.sun_path, "/run/fpll.sock");

	unlink(addr.sun_path);
	int ret = bind(listen_fd, (struct sockaddr *) &addr, sizeof(addr));
	if (ret == -1) {
		fprintf(stderr, "failed to bind\n");
		return;
	}

	ret = listen(listen_fd, 1);
	if (ret == -1) {
		fprintf(stderr, "failed to listen\n");
		return;
	}


	// single-threaded server
	while (true) {
		int conn_fd = accept4(listen_fd, NULL, NULL, SOCK_CLOEXEC);
		if (conn_fd == -1) {
			fprintf(stderr, "failed to accept\n");
			return;
		}

		std::thread(serve_rpc_conn, se, conn_fd).detach();
	}
}

int main(int argc, char *argv[])
{
	struct fuse_args args = FUSE_ARGS_INIT(argc, argv);
	struct fuse_session *se;
	struct fuse_cmdline_opts opts;
	struct fuse_loop_config config;
	struct lo_data lo = { .debug = 0,
	                      .writeback = 0 };
	int ret = -1;

	/* Don't mask creation mode, kernel already did that */
	umask(0);

	pthread_mutex_init(&lo.mutex, NULL);
	lo.root.fd = -1;
	lo.cache = CACHE_NORMAL;

	if (fuse_parse_cmdline(&args, &opts) != 0)
		return 1;
	if (opts.show_help) {
		ret = 0;
		goto err_out1;
	} else if (opts.show_version) {
		ret = 0;
		goto err_out1;
	}

	if(opts.mountpoint == NULL) {
		ret = 1;
		goto err_out1;
	}

	if (fuse_opt_parse(&args, &lo, lo_opts, NULL)== -1)
		return 1;

	lo.debug = opts.debug;
	lo.root.refcount = 2;
	if (lo.source) {
		struct stat stat;
		int res;

		res = lstat(lo.source, &stat);
		if (res == -1) {
			fuse_log(FUSE_LOG_ERR, "failed to stat source (\"%s\"): %m\n",
				 lo.source);
			exit(1);
		}
		if (!S_ISDIR(stat.st_mode)) {
			fuse_log(FUSE_LOG_ERR, "source is not a directory\n");
			exit(1);
		}

	} else {
		lo.source = strdup("/");
		if(!lo.source) {
			fuse_log(FUSE_LOG_ERR, "fuse: memory allocation failed\n");
			exit(1);
		}
	}
	if (!lo.timeout_set) {
		switch (lo.cache) {
		case CACHE_NEVER:
			lo.timeout = 0.0;
			break;

		case CACHE_NORMAL:
			lo.timeout = 1.0;
			break;

		case CACHE_ALWAYS:
			lo.timeout = 86400.0;
			break;
		}
	} else if (lo.timeout < 0) {
		fuse_log(FUSE_LOG_ERR, "timeout is negative (%lf)\n",
			 lo.timeout);
		exit(1);
	}

	lo.root.fd = open(lo.source, O_PATH);
	if (lo.root.fd == -1) {
		fuse_log(FUSE_LOG_ERR, "open(\"%s\", O_PATH): %m\n",
			 lo.source);
		exit(1);
	}

	struct stat st;
	if (fstat(lo.root.fd, &st) == -1) {
		fuse_log(FUSE_LOG_ERR, "fstat(\"%s\"): %m\n",
			 lo.source);
		exit(1);
	}
	root_inode_key = { st.st_dev, st.st_ino };
	ino_to_ptr[hash_st_ino(st.st_dev, st.st_ino)] = &lo.root;

	se = fuse_session_new(&args, &lo_oper, sizeof(lo_oper), &lo);
	if (se == NULL)
	    goto err_out1;

	if (fuse_set_signal_handlers(se) != 0)
	    goto err_out2;

	if (fuse_session_mount(se, opts.mountpoint) != 0)
	    goto err_out3;

	fuse_daemonize(opts.foreground);

	// XXX: it's NOT safe to stop this server!
	// RPC server does not safely setop using 'se' before fuse_session_destroy
	// so that could cause crashes
	std::thread(listen_rpc, se).detach();

	/* Block until ctrl+c or fusermount -u */
	if (opts.singlethread)
		ret = fuse_session_loop(se);
	else {
		config.clone_fd = opts.clone_fd;
		config.max_idle_threads = opts.max_idle_threads;
		ret = fuse_session_loop_mt(se, &config);
	}

	fuse_session_unmount(se);
err_out3:
	fuse_remove_signal_handlers(se);
err_out2:
	fuse_session_destroy(se);
err_out1:
	free(opts.mountpoint);
	fuse_opt_free_args(&args);

	if (lo.root.fd >= 0)
		close(lo.root.fd);

	free(lo.source);
	return ret ? 1 : 0;
}
