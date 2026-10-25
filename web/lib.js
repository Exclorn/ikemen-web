// Library of uploaded zips, kept in the browser's IndexedDB so they survive reloads.
(function () {
	const DB = "ikemen-library", STORE = "zips";
	const open = () => new Promise((res, rej) => {
		const r = indexedDB.open(DB, 1);
		r.onupgradeneeded = () => r.result.createObjectStore(STORE, { keyPath: "name" });
		r.onsuccess = () => res(r.result);
		r.onerror = () => rej(r.error);
	});
	const wrap = req => new Promise((res, rej) => { req.onsuccess = () => res(req.result); req.onerror = () => rej(req.error); });
	const store = async mode => (await open()).transaction(STORE, mode).objectStore(STORE);

	globalThis.ikemenLib = {
		// Records without the file data: {name, size, enabled, added}
		async list() {
			const all = await wrap((await store("readonly")).getAll());
			return all.map(({ name, size, enabled, added, chars, stages, hidden }) => ({ name, size, enabled, added, chars, stages, hidden: hidden || [] })).sort((a, b) => a.added - b.added);
		},
		async add(file, meta) {
			const old = await wrap((await store("readonly")).get(file.name));
			await wrap((await store("readwrite")).put({
				name: file.name, size: file.size, blob: file, enabled: old ? old.enabled : true, added: old ? old.added : Date.now(),
				chars: meta ? meta.chars : undefined, stages: meta ? meta.stages : undefined, hidden: old && old.hidden ? old.hidden : [],
			}));
		},
		async remove(name) { await wrap((await store("readwrite")).delete(name)); },
		async setEnabled(name, enabled) {
			const s = await store("readwrite");
			const rec = await wrap(s.get(name));
			if (rec) { rec.enabled = enabled; await wrap(s.put(rec)); }
		},
		async update(name, fields) {
			const s = await store("readwrite");
			const rec = await wrap(s.get(name));
			if (rec) await wrap(s.put(Object.assign(rec, fields)));
		},
		async blob(name) { const r = await wrap((await store("readonly")).get(name)); return r ? r.blob : null; },
		async usage() {
			try { const e = await navigator.storage.estimate(); return { used: e.usage, quota: e.quota }; } catch (_) { return null; }
		},
		persist() { try { return navigator.storage.persist(); } catch (_) { return Promise.resolve(false); } },
	};
})();
