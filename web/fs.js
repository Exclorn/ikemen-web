// In-memory filesystem for the browser build.
//
// Go's js/wasm runtime implements package os on top of a Node.js style `fs` object
// (globalThis.fs) and a `process` object. This file provides both, backed by memory.
// Paths are case-insensitive but keep the case they were created with, because a lot of
// MUGEN content was made for Windows and refers to files with the wrong case.
//
// Load this file BEFORE wasm_exec.js, which only installs its own stubs when these are missing.
"use strict";

(() => {
	// Linux flag values. Go passes them through unchanged, so any consistent set works.
	const O_WRONLY = 1, O_RDWR = 2, O_CREAT = 0o100, O_EXCL = 0o200, O_TRUNC = 0o1000,
		O_APPEND = 0o2000, O_DIRECTORY = 0o200000;
	const S_IFDIR = 0o040000, S_IFREG = 0o100000;

	const encoder = new TextEncoder();
	const decoder = new TextDecoder("utf-8");

	function fsError(code, message) {
		const err = new Error(message || code);
		err.code = code;
		return err;
	}

	// --- Paths ---------------------------------------------------------------

	let cwd = "/";

	function normalize(p) {
		const out = [];
		for (const part of p.split("/")) {
			if (part === "" || part === ".") continue;
			if (part === "..") { out.pop(); continue; }
			out.push(part);
		}
		return "/" + out.join("/");
	}

	function resolve(...segments) {
		let p = "";
		for (let i = segments.length - 1; i >= 0; i--) {
			const s = String(segments[i]);
			if (s === "") continue;
			p = p === "" ? s : s + "/" + p;
			if (s.startsWith("/")) break;
		}
		if (!p.startsWith("/")) p = cwd + "/" + p;
		return normalize(p.replace(/\\/g, "/"));
	}

	const keyOf = (absPath) => absPath.toLowerCase();

	// --- Tree ----------------------------------------------------------------

	class Node {
		constructor(name, isDir) {
			this.name = name;          // name with its original case
			this.isDir = isDir;
			this.data = isDir ? null : new Uint8Array(0);
			this.size = 0;             // bytes used in data
			this.children = isDir ? new Map() : null; // lowercase name -> Node
			this.mtimeMs = Date.now();
			this.ino = ++Node.nextIno;
		}
	}
	Node.nextIno = 1;

	const root = new Node("", true);

	// Called whenever something under a persistent directory changes
	let onChange = null;

	function lookup(absPath) {
		if (absPath === "/") return root;
		let node = root;
		for (const part of absPath.split("/").slice(1)) {
			if (!node.isDir) return null;
			node = node.children.get(part.toLowerCase());
			if (!node) return null;
		}
		return node;
	}

	function splitParent(absPath) {
		const i = absPath.lastIndexOf("/");
		return [absPath.slice(0, i) || "/", absPath.slice(i + 1)];
	}

	function mkdirp(absPath) {
		let node = root;
		for (const part of absPath.split("/").slice(1)) {
			let next = node.children.get(part.toLowerCase());
			if (!next) {
				next = new Node(part, true);
				node.children.set(part.toLowerCase(), next);
			} else if (!next.isDir) {
				throw fsError("ENOTDIR");
			}
			node = next;
		}
		return node;
	}

	function ensureCapacity(node, n) {
		if (node.data.length >= n) return;
		let cap = Math.max(n, 256, Math.floor(node.data.length * 1.5));
		const bigger = new Uint8Array(cap);
		bigger.set(node.data.subarray(0, node.size));
		node.data = bigger;
	}

	function makeStats(node) {
		const isDir = node.isDir;
		const size = isDir ? 4096 : node.size;
		return {
			dev: 1, ino: node.ino,
			mode: (isDir ? S_IFDIR | 0o755 : S_IFREG | 0o644),
			nlink: 1, uid: 0, gid: 0, rdev: 0,
			size: size, blksize: 4096, blocks: Math.ceil(size / 512),
			atimeMs: node.mtimeMs, mtimeMs: node.mtimeMs, ctimeMs: node.mtimeMs,
			isDirectory() { return isDir; },
			isFile() { return !isDir; },
		};
	}

	// --- File descriptors ------------------------------------------------------

	const fds = new Map();
	let nextFd = 3;

	function getFd(fd) {
		const f = fds.get(fd);
		if (!f) throw fsError("EBADF");
		return f;
	}

	// Run a synchronous implementation and report through a Node style callback
	function wrap(fn) {
		return (...args) => {
			const cb = args.pop();
			let result;
			try {
				result = fn(...args);
			} catch (e) {
				if (e && e.code) { cb(e); return; }
				throw e;
			}
			// Go's fsCall treats a missing second argument as undefined
			if (result === undefined) cb(null); else cb(null, result);
		};
	}

	// --- Console output (stdout / stderr) ----------------------------------------

	let outputBuf = "";
	function writeConsole(fd, buf) {
		outputBuf += decoder.decode(buf, { stream: true });
		const nl = outputBuf.lastIndexOf("\n");
		if (nl != -1) {
			const text = outputBuf.substring(0, nl);
			if (fd === 2) console.error(text); else console.log(text);
			outputBuf = outputBuf.substring(nl + 1);
		}
		return buf.length;
	}

	// --- The fs object -------------------------------------------------------

	const fs = {
		constants: { O_WRONLY, O_RDWR, O_CREAT, O_EXCL, O_TRUNC, O_APPEND, O_DIRECTORY },

		writeSync(fd, buf) {
			if (fd === 1 || fd === 2) return writeConsole(fd, buf);
			return fsImpl.write(fd, buf, 0, buf.length, null);
		},

		write: wrap((fd, buf, offset, length, position) => fsImpl.write(fd, buf, offset, length, position)),
		read: wrap((fd, buffer, offset, length, position) => fsImpl.read(fd, buffer, offset, length, position)),

		open: wrap((path, flags, mode) => fsImpl.open(path, flags, mode)),
		close: wrap((fd) => { getFd(fd); fds.delete(fd); }),

		stat: wrap((path) => {
			const node = lookup(resolve(path));
			if (!node) throw fsError("ENOENT");
			return makeStats(node);
		}),
		lstat: wrap((path) => {
			const node = lookup(resolve(path));
			if (!node) throw fsError("ENOENT");
			return makeStats(node);
		}),
		fstat: wrap((fd) => {
			if (fd >= 0 && fd <= 2) {
				return makeStats(new Node("", false));
			}
			return makeStats(getFd(fd).node);
		}),

		readdir: wrap((path) => {
			const node = lookup(resolve(path));
			if (!node) throw fsError("ENOENT");
			if (!node.isDir) throw fsError("ENOTDIR");
			return Array.from(node.children.values(), (n) => n.name);
		}),

		mkdir: wrap((path, perm) => {
			const abs = resolve(path);
			if (lookup(abs)) throw fsError("EEXIST");
			const [parentPath, name] = splitParent(abs);
			const parent = lookup(parentPath);
			if (!parent) throw fsError("ENOENT");
			if (!parent.isDir) throw fsError("ENOTDIR");
			parent.children.set(name.toLowerCase(), new Node(name, true));
			parent.mtimeMs = Date.now();
			notify(abs);
		}),

		rmdir: wrap((path) => {
			const abs = resolve(path);
			const node = lookup(abs);
			if (!node) throw fsError("ENOENT");
			if (!node.isDir) throw fsError("ENOTDIR");
			if (node.children.size > 0) throw fsError("ENOTEMPTY");
			if (abs === "/") throw fsError("EBUSY");
			const [parentPath, name] = splitParent(abs);
			lookup(parentPath).children.delete(name.toLowerCase());
			notify(abs);
		}),

		unlink: wrap((path) => {
			const abs = resolve(path);
			const node = lookup(abs);
			if (!node) throw fsError("ENOENT");
			if (node.isDir) throw fsError("EISDIR");
			const [parentPath, name] = splitParent(abs);
			lookup(parentPath).children.delete(name.toLowerCase());
			notify(abs);
		}),

		rename: wrap((from, to) => {
			const a = resolve(from), b = resolve(to);
			const node = lookup(a);
			if (!node) throw fsError("ENOENT");
			const [toParentPath, toName] = splitParent(b);
			const toParent = lookup(toParentPath);
			if (!toParent) throw fsError("ENOENT");
			if (!toParent.isDir) throw fsError("ENOTDIR");
			const existing = lookup(b);
			if (existing) {
				if (existing.isDir && existing.children.size > 0) throw fsError("ENOTEMPTY");
				if (existing.isDir !== node.isDir) throw fsError(node.isDir ? "ENOTDIR" : "EISDIR");
			}
			const [fromParentPath, fromName] = splitParent(a);
			lookup(fromParentPath).children.delete(fromName.toLowerCase());
			node.name = toName;
			toParent.children.set(toName.toLowerCase(), node);
			notify(a);
			notify(b);
		}),

		ftruncate: wrap((fd, length) => {
			const f = getFd(fd);
			if (f.node.isDir) throw fsError("EISDIR");
			truncateNode(f.node, length);
			notify(f.path);
		}),
		truncate: wrap((path, length) => {
			const abs = resolve(path);
			const node = lookup(abs);
			if (!node) throw fsError("ENOENT");
			if (node.isDir) throw fsError("EISDIR");
			truncateNode(node, length);
			notify(abs);
		}),

		fsync: wrap(() => {}),
		utimes: wrap(() => {}),
		chmod: wrap(() => {}),
		fchmod: wrap(() => {}),
		chown: wrap(() => {}),
		fchown: wrap(() => {}),
		lchown: wrap(() => {}),

		link: (a, b, cb) => cb(fsError("ENOSYS")),
		symlink: (a, b, cb) => cb(fsError("ENOSYS")),
		readlink: (p, cb) => cb(fsError("EINVAL")),
	};

	function truncateNode(node, length) {
		if (length < node.size) {
			node.size = length;
		} else if (length > node.size) {
			ensureCapacity(node, length);
			node.data.fill(0, node.size, length);
			node.size = length;
		}
		node.mtimeMs = Date.now();
	}

	const fsImpl = {
		open(path, flags, mode) {
			const abs = resolve(path);
			let node = lookup(abs);
			const writable = (flags & (O_WRONLY | O_RDWR)) !== 0;
			if (node) {
				if ((flags & O_CREAT) && (flags & O_EXCL)) throw fsError("EEXIST");
				if (node.isDir && writable) throw fsError("EISDIR");
				if (!node.isDir && (flags & O_DIRECTORY)) throw fsError("ENOTDIR");
			} else {
				if (!(flags & O_CREAT)) throw fsError("ENOENT");
				const [parentPath, name] = splitParent(abs);
				const parent = lookup(parentPath);
				if (!parent) throw fsError("ENOENT");
				if (!parent.isDir) throw fsError("ENOTDIR");
				node = new Node(name, false);
				parent.children.set(name.toLowerCase(), node);
				notify(abs);
			}
			if ((flags & O_TRUNC) && writable && !node.isDir) {
				truncateNode(node, 0);
				notify(abs);
			}
			const fd = nextFd++;
			fds.set(fd, { node, path: abs, flags, pos: 0, writable });
			return fd;
		},

		read(fd, buffer, offset, length, position) {
			const f = getFd(fd);
			if (f.node.isDir) throw fsError("EISDIR");
			const node = f.node;
			const start = position === null || position === undefined ? f.pos : position;
			const n = Math.max(0, Math.min(length, node.size - start));
			if (n > 0) buffer.set(node.data.subarray(start, start + n), offset);
			if (position === null || position === undefined) f.pos += n;
			return n;
		},

		write(fd, buffer, offset, length, position) {
			if (fd === 1 || fd === 2) return writeConsole(fd, buffer.subarray(offset, offset + length));
			const f = getFd(fd);
			if (f.node.isDir) throw fsError("EISDIR");
			if (!f.writable) throw fsError("EBADF");
			const node = f.node;
			let start;
			if (f.flags & O_APPEND) {
				start = node.size;
			} else {
				start = position === null || position === undefined ? f.pos : position;
			}
			const end = start + length;
			ensureCapacity(node, end);
			if (start > node.size) node.data.fill(0, node.size, start);
			node.data.set(buffer.subarray(offset, offset + length), start);
			if (end > node.size) node.size = end;
			node.mtimeMs = Date.now();
			if (position === null || position === undefined) f.pos = end;
			notify(f.path);
			return length;
		},
	};

	// --- Process ----------------------------------------------------------------

	const process = {
		getuid() { return 0; },
		getgid() { return 0; },
		geteuid() { return 0; },
		getegid() { return 0; },
		getgroups() { return []; },
		pid: 1,
		ppid: 0,
		umask() { return 0o022; },
		cwd() { return cwd; },
		chdir(dir) {
			const abs = resolve(dir);
			const node = lookup(abs);
			if (!node) throw fsError("ENOENT");
			if (!node.isDir) throw fsError("ENOTDIR");
			cwd = abs;
		},
	};

	const path = { resolve };

	// --- Public helpers -----------------------------------------------------------

	function notify(absPath) {
		if (onChange) onChange(absPath);
	}

	const api = {
		// Writes a file, creating parent folders. Used to load game content.
		writeFile(p, bytes) {
			const abs = resolve(p);
			const [parentPath, name] = splitParent(abs);
			const parent = mkdirp(parentPath);
			let node = parent.children.get(name.toLowerCase());
			if (node && node.isDir) throw fsError("EISDIR");
			if (!node) {
				node = new Node(name, false);
				parent.children.set(name.toLowerCase(), node);
			}
			// The array is used as is, without copying
			node.data = bytes;
			node.size = bytes.length;
			node.mtimeMs = Date.now();
			return node;
		},
		readFile(p) {
			const node = lookup(resolve(p));
			if (!node || node.isDir) return null;
			return node.data.subarray(0, node.size);
		},
		exists(p) { return lookup(resolve(p)) !== null; },
		mkdirp(p) { mkdirp(resolve(p)); },
		// Lists all files below a folder as [path, bytes] pairs
		listFiles(dir) {
			const out = [];
			const walk = (node, prefix) => {
				for (const child of node.children.values()) {
					const p = prefix + "/" + child.name;
					if (child.isDir) walk(child, p); else out.push([p, child.data.subarray(0, child.size)]);
				}
			};
			const start = lookup(resolve(dir));
			if (start && start.isDir) walk(start, resolve(dir) === "/" ? "" : resolve(dir));
			return out;
		},
		removeTree(dir) {
			const abs = resolve(dir);
			if (abs === "/") return;
			const [parentPath, name] = splitParent(abs);
			const parent = lookup(parentPath);
			if (parent && parent.children) parent.children.delete(name.toLowerCase());
		},
		// The callback gets the path of every changed file or folder
		setChangeListener(fn) { onChange = fn; },
		resolve,
	};

	globalThis.fs = fs;
	globalThis.process = process;
	globalThis.path = path;
	globalThis.ikemenFS = api;
})();
