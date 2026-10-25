// Minimal zip reader (stored and deflate entries) using DecompressionStream.
// filter(name, size) may return false to skip unpacking an entry; onEntry then gets null data.
globalThis.ikemenUnzip = async function (buf, onEntry, filter) {
	const u8 = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
	const dv = new DataView(u8.buffer, u8.byteOffset, u8.byteLength);
	let eocd = -1;
	for (let i = u8.length - 22; i >= Math.max(0, u8.length - 65557); i--) {
		if (dv.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
	}
	if (eocd < 0) throw new Error("Not a zip file");
	const count = dv.getUint16(eocd + 10, true);
	let p = dv.getUint32(eocd + 16, true);
	const dec = new TextDecoder();
	const entries = [];
	for (let n = 0; n < count; n++) {
		if (dv.getUint32(p, true) !== 0x02014b50) throw new Error("Bad zip directory");
		const flags = dv.getUint16(p + 8, true);
		const method = dv.getUint16(p + 10, true);
		const csize = dv.getUint32(p + 20, true);
		const usize = dv.getUint32(p + 24, true);
		const nlen = dv.getUint16(p + 28, true), elen = dv.getUint16(p + 30, true), clen = dv.getUint16(p + 32, true);
		const lho = dv.getUint32(p + 42, true);
		const name = dec.decode(u8.subarray(p + 46, p + 46 + nlen));
		entries.push({ name, method, csize, usize, lho });
		p += 46 + nlen + elen + clen;
	}
	let done = 0;
	for (const e of entries) {
		if (e.name.endsWith("/")) { done++; continue; }
		if (filter && !filter(e.name, e.usize)) { onEntry(e.name, null); done++; continue; }
		const lnlen = dv.getUint16(e.lho + 26, true), lelen = dv.getUint16(e.lho + 28, true);
		const start = e.lho + 30 + lnlen + lelen;
		const raw = u8.subarray(start, start + e.csize);
		let data;
		if (e.method === 0) data = raw.slice();
		else if (e.method === 8) {
			const ds = new Blob([raw]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
			data = new Uint8Array(await new Response(ds).arrayBuffer());
		} else throw new Error("Unsupported zip method " + e.method + " for " + e.name);
		onEntry(e.name, data);
		if (++done % 50 === 0) await new Promise(r => setTimeout(r, 0));
	}
	return entries.length;
};
