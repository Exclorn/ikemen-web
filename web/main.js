(function () {
	const $ = id => document.getElementById(id);
	const status = t => { $("status").textContent = t; };
	// Mirror console output and errors into an on-page log so problems can be screenshotted
	const logEl = $("log"), lines = [];
	const addLog = (k, a) => {
		lines.push((k === "log" ? "" : k.toUpperCase() + ": ") + a.map(x => x instanceof Error ? x.stack || x.message : String(x)).join(" "));
		if (lines.length > 80) lines.shift();
		logEl.textContent = lines.join("\n");
		logEl.parentNode.scrollTop = 1e9;
	};
	for (const k of ["log", "info", "warn", "error"]) {
		const o = console[k].bind(console);
		console[k] = (...a) => { o(...a); addLog(k, a); };
	}
	window.addEventListener("error", e => addLog("error", [e.message + " @" + e.filename + ":" + e.lineno]));
	window.addEventListener("unhandledrejection", e => addLog("error", ["Unhandled: " + (e.reason && (e.reason.stack || e.reason))]));
	$("logtoggle").addEventListener("click", () => {
		const hide = logEl.style.display !== "none";
		logEl.style.display = hide ? "none" : "";
		$("logtoggle").textContent = hide ? "Show log" : "Hide log";
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

	async function fetchZip(url, label, strip) {
		status("Downloading " + label + "...");
		const r = await fetch(url + "?v=" + Math.floor(Date.now() / 60000));
		if (!r.ok) throw new Error(url + ": HTTP " + r.status);
		const buf = await r.arrayBuffer();
		status("Unpacking " + label + "...");
		await load(buf, strip, label);
	}
	// Unpacks a zip into the virtual filesystem and returns the list of [path, bytes] it wrote.
	// For user zips (strip = true) a single wrapper folder is removed, and a zip that has no chars/ or
	// stages/ folder at all is treated as one character folder and placed under chars/.
	// Works out the final path of every file in a user zip: removes one wrapper folder and wraps plain
	// character zips under chars/. names is a list of [path, bytes|null].
	function normalize(names, zipName) {
		const known = ["data", "chars", "stages", "font", "sound"];
		const topOf = n => n.split("/")[0].toLowerCase();
		let prefix = "";
		// Remove one shared top-level folder, e.g. "MyPack/chars/..."
		const first = names.length ? names[0][0].split("/")[0] : "";
		if (first && names.every(([n]) => n.startsWith(first + "/")) && !known.includes(first.toLowerCase()))
			prefix = first + "/";
		let out = names.map(([n, d]) => [n.slice(prefix.length), d]);
		if (!out.some(([n]) => known.includes(topOf(n)))) {
			// A plain character zip: use the wrapper folder name (or the zip name) as the folder
			const folder = prefix ? prefix.slice(0, -1) : (zipName || "char").replace(/\.zip$/i, "");
			out = out.map(([n, d]) => ["chars/" + folder + "/" + n, d]);
		}
		return out;
	}
	const cleanName = n => n.replace(/\\/g, "/").replace(/^\/+/, "");
	// MacOS leftovers, hidden files and path tricks
	const junk = n => {
		const base = n.split("/").pop();
		return /^__MACOSX\//i.test(n) || base.startsWith("._") || base === ".DS_Store" || base === "Thumbs.db" || n.split("/").includes("..");
	};

	// Unpacks a zip into the virtual filesystem and returns the list of [path, bytes] it wrote.
	async function load(buf, strip, zipName) {
		const names = [];
		await ikemenUnzip(buf, (n, d) => {
			n = cleanName(n);
			if (strip && junk(n)) return;
			names.push([n, d]);
		});
		const overwritten = [];
		const out = strip ? normalize(names, zipName) : names;
		for (const [n, d] of out) {
			if (strip && n.toLowerCase() === "data/select.def") continue; // never replace the game's own list
			if (strip && ikemenFS.exists("/" + n) && /\.(def|sff|air|cmd|cns)$/i.test(n)) overwritten.push(n);
			ikemenFS.writeFile("/" + n, d);
		}
		if (strip && overwritten.length) console.warn((zipName || "zip") + " replaced " + overwritten.length + " existing file(s), e.g. " + overwritten[0]);
		return out;
	}

	// Lists the characters and stages in a user zip without unpacking everything (only .def files are read)
	async function inspectZip(blob, zipName) {
		const names = [];
		await ikemenUnzip(await blob.arrayBuffer(), (n, d) => {
			n = cleanName(n);
			if (!junk(n)) names.push([n, d]);
		}, (n, size) => /\.def$/i.test(n) && size <= 1 << 20);
		const found = ikemenSelectDef.scan(normalize(names, zipName).filter(([, d]) => d));
		return { chars: found.chars, stages: found.stages };
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
			await fetchZip("engine.zip", "engine", false);
			await fetchZip("content.zip", "content", false);
			const chars = [], stages = [];
			for (const rec of await ikemenLib.list()) {
				if (!rec.enabled) continue;
				status("Unpacking " + rec.name + "...");
				try {
					const out = await load(await (await ikemenLib.blob(rec.name)).arrayBuffer(), true, rec.name);
					const found = ikemenSelectDef.scan(out);
					const hidden = new Set(rec.hidden || []);
					chars.push(...found.chars.filter(c => !hidden.has("c:" + c)));
					stages.push(...found.stages.filter(s => !hidden.has("s:" + s)));
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
