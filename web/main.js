(function () {
	const $ = id => document.getElementById(id);
	const status = t => { $("status").textContent = t; };
	// Mirror console output into an on-page log. It stays closed unless something serious happens,
	// so the many harmless engine warnings do not get in the way.
	const logEl = $("log"), box = $("logbox"), lines = [];
	const setLog = open => {
		box.classList.toggle("open", open);
		$("logtoggle").textContent = open ? "Hide log" : "Log";
		if (open) box.scrollTop = 1e9;
	};
	const serious = /\bpanic\b|goroutine \d+ \[running\]|^Unhandled|^Uncaught/i;
	const addLog = (prefix, a) => {
		const text = prefix + a.map(x => x instanceof Error ? x.stack || x.message : String(x)).join(" ");
		lines.push(text);
		if (lines.length > 200) lines.shift();
		logEl.textContent = lines.join("\n");
		if (box.classList.contains("open")) box.scrollTop = 1e9;
		else if (serious.test(text)) setLog(true);
	};
	for (const k of ["log", "info", "warn", "error"]) {
		const o = console[k].bind(console);
		console[k] = (...a) => { o(...a); addLog("", a); };
	}
	window.addEventListener("error", e => addLog("Uncaught: ", [e.message + " @" + e.filename + ":" + e.lineno]));
	window.addEventListener("unhandledrejection", e => addLog("Unhandled: ", [e.reason && (e.reason.stack || e.reason)]));
	$("logtoggle").addEventListener("click", () => setLog(!box.classList.contains("open")));
	$("logcopy").addEventListener("click", async () => {
		try { await navigator.clipboard.writeText(lines.join("\n")); $("logcopy").textContent = "Copied"; }
		catch (_) { $("logcopy").textContent = "Copy failed"; }
		setTimeout(() => { $("logcopy").textContent = "Copy"; }, 1500);
	});
	const DB = "ikemen-save", STORE = "files";

	function idb() {
		return new Promise((res, rej) => {
			const r = indexedDB.open(DB, 1);
			r.onupgradeneeded = () => r.result.createObjectStore(STORE);
			r.onsuccess = () => res(r.result);
			r.onerror = () => rej(r.error);
		});
	}
	async function restoreSave() {
		try {
			const db = await idb();
			await new Promise((res, rej) => {
				const q = db.transaction(STORE).objectStore(STORE).openCursor();
				q.onsuccess = () => { const c = q.result; if (!c) return res(); ikemenFS.writeFile(c.key, c.value); c.continue(); };
				q.onerror = () => rej(q.error);
			});
		} catch (e) { console.warn("restore failed", e); }
	}
	let timer = 0;
	function scheduleSave() {
		clearTimeout(timer);
		timer = setTimeout(async () => {
			try {
				const db = await idb();
				const tx = db.transaction(STORE, "readwrite");
				const s = tx.objectStore(STORE);
				s.clear();
				for (const [p, b] of ikemenFS.listFiles("/save")) {
					if (p.startsWith("/save/logs/")) continue;
					s.put(b.slice(), p);
				}
			} catch (e) { console.warn("save failed", e); }
		}, 1500);
	}

	async function fetchZip(url, label) {
		status("Downloading " + label + "...");
		const r = await fetch(url + "?v=" + Math.floor(Date.now() / 60000));
		if (!r.ok) throw new Error(url + ": HTTP " + r.status);
		const blob = await r.blob();
		status("Unpacking " + label + "...");
		const list = await ikemenZipList(blob);
		const want = new Set(list.map(e => e.name));
		await ikemenUnzip(blob, (n, d) => ikemenFS.writeFile("/" + n, d), n => want.has(n));
	}

	// Works out the final path of every file in a user zip: removes one wrapper folder and wraps plain
	// character zips under chars/. entries are [path, data, originalName].
	function normalize(entries, zipName) {
		const known = ["data", "chars", "stages", "font", "sound"];
		const topOf = n => n.split("/")[0].toLowerCase();
		let prefix = "";
		// Remove one shared top-level folder, e.g. "MyPack/chars/..."
		const first = entries.length ? entries[0][0].split("/")[0] : "";
		if (first && entries.every(([n]) => n.startsWith(first + "/")) && !known.includes(first.toLowerCase()))
			prefix = first + "/";
		let out = entries.map(([n, d, o]) => [n.slice(prefix.length), d, o]);
		if (!out.some(([n]) => known.includes(topOf(n)))) {
			// A plain character zip: use the wrapper folder name (or the zip name) as the folder
			const folder = prefix ? prefix.slice(0, -1) : (zipName || "char").replace(/\.zip$/i, "");
			out = out.map(([n, d, o]) => ["chars/" + folder + "/" + n, d, o]);
		}
		return out;
	}
	const cleanName = n => n.replace(/\\/g, "/").replace(/^\/+/, "");
	// MacOS leftovers, hidden files and path tricks
	const junk = n => {
		const base = n.split("/").pop();
		return /^__MACOSX\//i.test(n) || base.startsWith("._") || base === ".DS_Store" || base === "Thumbs.db" || n.split("/").includes("..");
	};

	// Reads a user zip's directory and its .def files (nothing else) and finds the characters and stages in it.
	async function planZip(src, zipName) {
		const entries = [];
		for (const e of await ikemenZipList(src)) {
			const n = cleanName(e.name);
			if (!junk(n)) entries.push([n, null, e.name]);
		}
		const out = normalize(entries, zipName);
		const defs = new Map();
		await ikemenUnzip(src, (o, d) => defs.set(o, d), (o, size) => /\.def$/i.test(o) && size <= 1 << 20);
		const found = ikemenSelectDef.scan(out.filter(([, , o]) => defs.has(o)).map(([p, , o]) => [p, defs.get(o)]));
		return { out, defs, found };
	}
	async function inspectZip(blob, zipName) {
		return (await planZip(blob, zipName)).found;
	}

	// Unpacks a user zip into the virtual filesystem. Characters and stages the user switched off are not
	// unpacked at all. Returns the characters and stages that are switched on.
	async function loadUserZip(blob, zipName, hiddenList) {
		const hidden = new Set(hiddenList || []);
		const { out, defs, found } = await planZip(blob, zipName);
		const chars = found.chars.filter(c => !hidden.has("c:" + c));
		const stages = found.stages.filter(s => !hidden.has("s:" + s));
		// A character folder is skipped when it has hidden characters and no visible ones
		const folder = c => "chars/" + c.split("/")[0] + "/";
		const keepFolders = new Set(chars.map(folder).map(f => f.toLowerCase()));
		const skipFolders = [...new Set(found.chars.filter(c => hidden.has("c:" + c)).map(folder).map(f => f.toLowerCase()))]
			.filter(f => !keepFolders.has(f));
		const skipStages = found.stages.filter(s => hidden.has("s:" + s)).map(s => s.toLowerCase().replace(/\.def$/, ""));
		const skipped = p => {
			const l = p.toLowerCase();
			if (l === "data/select.def") return true; // never replace the game's own list
			return skipFolders.some(f => l.startsWith(f)) || skipStages.some(s => l.startsWith(s + ".") || l.startsWith(s + "/"));
		};
		const wanted = new Map(out.filter(([p]) => !skipped(p)).map(([p, , o]) => [o, p]));
		const overwritten = [];
		const write = (o, d) => {
			const p = wanted.get(o);
			if (ikemenFS.exists("/" + p) && /\.(def|sff|air|cmd|cns)$/i.test(p)) overwritten.push(p);
			ikemenFS.writeFile("/" + p, d);
		};
		for (const [o, d] of defs) if (wanted.has(o)) write(o, d);
		await ikemenUnzip(blob, write, o => wanted.has(o) && !defs.has(o));
		if (overwritten.length) console.warn(zipName + " replaced " + overwritten.length + " existing file(s), e.g. " + overwritten[0]);
		const skippedCount = out.length - wanted.size - (out.some(([p]) => p.toLowerCase() === "data/select.def") ? 1 : 0);
		if (skippedCount > 0) console.log(zipName + ": skipped " + skippedCount + " file(s) of switched-off characters/stages");
		return { chars, stages };
	}

	window.ikemenShowMessage = (level, title, msg) => {
		console.log("[" + level + "] " + title + ": " + msg);
		if (level === "error") { $("overlay").hidden = false; $("errtext").textContent = title + "\n\n" + msg; }
	};

	let started = false;
	async function start() {
		if (started) return; started = true;
		$("start").disabled = true;
		try {
			// Audio must be created inside the click
			const AC = window.AudioContext || window.webkitAudioContext;
			window.ikemenAudioCtx = new AC({ latencyHint: "interactive" });
			ikemenAudioCtx.resume();
			await fetchZip("engine.zip", "engine");
			await fetchZip("content.zip", "content");
			const chars = [], stages = [];
			for (const rec of await ikemenLib.list()) {
				if (!rec.enabled) continue;
				status("Unpacking " + rec.name + "...");
				try {
					const r = await loadUserZip(await ikemenLib.blob(rec.name), rec.name, rec.hidden);
					chars.push(...r.chars);
					stages.push(...r.stages);
				} catch (e) {
					console.error("Could not read " + rec.name + ": " + e.message);
				}
			}
			const added = chars.length || stages.length ? [1] : [];
			const selPath = "/data/select.def";
			let sel = ikemenFS.readFile(selPath);
			if (sel) {
				let text = new TextDecoder().decode(sel);
				if (added.length) {
					const found = { chars, stages };
					text = ikemenSelectDef.apply(text, found.chars, found.stages);
					console.log("Added " + found.chars.length + " character(s) and " + found.stages.length + " stage(s) to select.def");
					found.chars.forEach(c => console.log("  character: " + c));
					found.stages.forEach(s => console.log("  stage: " + s));
				}
				// Stages that use 3D models cannot be drawn in the browser (they would be a black screen)
				const model = [];
				for (const [p, d] of ikemenFS.listFiles("/stages")) {
					if (!/\.def$/i.test(p) || d.length > 1 << 20) continue;
					const t = new TextDecoder().decode(d);
					if (/^\s*\[model\]/im.test(t) || /\.glb\b/i.test(t)) model.push(p.slice(1));
				}
				if (model.length) {
					text = ikemenSelectDef.dropStages(text, model);
					console.log("Hidden 3D stages (not supported in the browser): " + model.join(", "));
				}
				ikemenFS.writeFile(selPath, new TextEncoder().encode(text));
			}
			await restoreSave();
			ikemenFS.setChangeListener(p => { if (p.startsWith("/save")) scheduleSave(); });
			status("Starting...");
			const go = new Go();
			const wasm = await WebAssembly.instantiateStreaming(fetch("ikemen.wasm?v=" + Date.now(), { cache: "no-store" }), go.importObject);
			$("menu").hidden = true;
			$("stage").hidden = false;
			$("ikemen-canvas").focus();
			await go.run(wasm.instance);
			status("The engine has exited. Reload the page to restart.");
			$("menu").hidden = false; $("stage").hidden = true;
		} catch (e) {
			console.error(e);
			status("Failed: " + e.message);
			setLog(true);
			$("start").disabled = false; started = false;
		}
	}

	// ---- Library UI ----
	const fmt = n => n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + " MB" : Math.max(1, Math.round(n / 1024)) + " KB";
	async function renderLib() {
		const box = $("libList");
		box.textContent = "";
		let recs = [];
		try { recs = await ikemenLib.list(); } catch (e) { box.textContent = "Browser storage is not available: " + e.message; return; }
		if (!recs.length) box.textContent = "Nothing added yet.";
		for (const r of recs) {
			if (!r.chars) {
				// Added before characters were listed: read the zip once and remember the result
				try {
					const meta = await inspectZip(await ikemenLib.blob(r.name), r.name);
					await ikemenLib.update(r.name, meta);
					Object.assign(r, meta);
				} catch (e) { r.chars = []; r.stages = []; }
			}
			const row = document.createElement("div");
			row.className = "libRow";
			const cb = document.createElement("input");
			cb.type = "checkbox"; cb.checked = r.enabled;
			cb.addEventListener("change", () => ikemenLib.setEnabled(r.name, cb.checked));
			const label = document.createElement("span");
			label.textContent = r.name + " (" + fmt(r.size) + ")";
			const del = document.createElement("button");
			del.textContent = "Remove";
			del.addEventListener("click", async () => { await ikemenLib.remove(r.name); renderLib(); });
			row.append(cb, label, del);
			box.append(row);
			// One switch per character/stage when a zip holds more than one
			const items = [...(r.chars || []).map(c => ["c:" + c, "Character: " + c]), ...(r.stages || []).map(s => ["s:" + s, "Stage: " + s])];
			if (items.length > 1) {
				const hidden = new Set(r.hidden || []);
				for (const [key, text] of items) {
					const sub = document.createElement("div");
					sub.className = "libRow libSub";
					const scb = document.createElement("input");
					scb.type = "checkbox"; scb.checked = !hidden.has(key);
					scb.addEventListener("change", () => {
						if (scb.checked) hidden.delete(key); else hidden.add(key);
						ikemenLib.update(r.name, { hidden: [...hidden] });
					});
					const sl = document.createElement("span");
					sl.textContent = text;
					sub.append(scb, sl);
					box.append(sub);
				}
			}
		}
		const u = await ikemenLib.usage();
		$("libUsage").textContent = u ? "Browser storage used: " + fmt(u.used) + " of " + fmt(u.quota) : "";
	}
	async function addFiles(files) {
		const zips = [...files].filter(f => /\.zip$/i.test(f.name));
		if (zips.length < files.length) status("Only .zip files can be added.");
		try {
			ikemenLib.persist();
			for (const f of zips) {
				let meta;
				try { meta = await inspectZip(f, f.name); } catch (e) { console.warn("Could not list " + f.name + ": " + e.message); }
				await ikemenLib.add(f, meta);
			}
		} catch (e) { status("Could not store the file: " + e.message); }
		renderLib();
	}
	$("extra").addEventListener("change", e => { addFiles(e.target.files); e.target.value = ""; });
	const drop = $("menu");
	drop.addEventListener("dragover", e => { e.preventDefault(); drop.classList.add("drag"); });
	drop.addEventListener("dragleave", () => drop.classList.remove("drag"));
	drop.addEventListener("drop", e => { e.preventDefault(); drop.classList.remove("drag"); addFiles(e.dataTransfer.files); });
	renderLib();
	$("start").addEventListener("click", start);
	$("fs").addEventListener("click", () => $("stage").requestFullscreen && $("stage").requestFullscreen());
	$("errclose").addEventListener("click", () => { $("overlay").hidden = true; });
	window.addEventListener("keydown", e => {
		if (!$("stage").hidden && (e.code.startsWith("Arrow") || e.code === "Space" || e.code === "Tab")) e.preventDefault();
	}, true);
})();
