// Minimal zip reader (stored and deflate entries) using DecompressionStream.
// The source can be a Blob (read in slices, so the whole file is never held in memory), an ArrayBuffer
// or a Uint8Array.
(function () {
	function reader(src) {
		if (typeof Blob !== "undefined" && src instanceof Blob) {
			return { size: src.size, read: async (s, e) => new Uint8Array(await src.slice(s, e).arrayBuffer()) };
		}
		const u8 = src instanceof Uint8Array ? src : new Uint8Array(src);
		return { size: u8.length, read: async (s, e) => u8.subarray(s, e) };
	}

	async function directory(r) {
		const tailStart = Math.max(0, r.size - 65557);
		const tail = await r.read(tailStart, r.size);
		const tv = new DataView(tail.buffer, tail.byteOffset, tail.byteLength);
		let eocd = -1;
		for (let i = tail.length - 22; i >= 0; i--) {
			if (tv.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
		}
		if (eocd < 0) throw new Error("Not a zip file");
		const count = tv.getUint16(eocd + 10, true);
		const cdSize = tv.getUint32(eocd + 12, true);
		const cdOffset = tv.getUint32(eocd + 16, true);
		if (count === 0xffff || cdOffset === 0xffffffff) throw new Error("Zip64 files are not supported");
		const cd = await r.read(cdOffset, cdOffset + cdSize);
		const dv = new DataView(cd.buffer, cd.byteOffset, cd.byteLength);
		const dec = new TextDecoder();
		const entries = [];
		let p = 0;
		for (let n = 0; n < count; n++) {
			if (dv.getUint32(p, true) !== 0x02014b50) throw new Error("Bad zip directory");
			const method = dv.getUint16(p + 10, true);
			const csize = dv.getUint32(p + 20, true);
			const usize = dv.getUint32(p + 24, true);
			const nlen = dv.getUint16(p + 28, true), elen = dv.getUint16(p + 30, true), clen = dv.getUint16(p + 32, true);
			const lho = dv.getUint32(p + 42, true);
			const name = dec.decode(cd.subarray(p + 46, p + 46 + nlen));
			entries.push({ name, method, csize, usize, lho });
			p += 46 + nlen + elen + clen;
		}
		return entries;
	}

	// Names and sizes of the files in a zip. Reads only the directory at the end of the file.
	globalThis.ikemenZipList = async function (src) {
		return (await directory(reader(src))).filter(e => !e.name.endsWith("/")).map(e => ({ name: e.name, usize: e.usize }));
	};

	// Unpacks entries. filter(name, size) may return false to skip an entry without reading it.
	globalThis.ikemenUnzip = async function (src, onEntry, filter) {
		const r = reader(src);
		const entries = await directory(r);
		let done = 0;
		for (const e of entries) {
			if (e.name.endsWith("/")) continue;
			if (filter && !filter(e.name, e.usize)) continue;
			const head = await r.read(e.lho, e.lho + 30);
			const hv = new DataView(head.buffer, head.byteOffset, head.byteLength);
			const start = e.lho + 30 + hv.getUint16(26, true) + hv.getUint16(28, true);
			const raw = await r.read(start, start + e.csize);
			let data;
			if (e.method === 0) data = raw.slice();
			else if (e.method === 8) {
				const ds = new Blob([raw]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
				data = new Uint8Array(await new Response(ds).arrayBuffer());
			} else throw new Error("Unsupported zip method " + e.method + " for " + e.name);
			onEntry(e.name, data);
			if (++done % 50 === 0) await new Promise(res => setTimeout(res, 0));
		}
		return entries.length;
	};
})();
