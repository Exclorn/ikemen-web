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
		await load(buf, strip);
	}
	async function load(buf, strip) {
		const names = [];
		await ikemenUnzip(buf, (n, d) => names.push([n, d]));
		let prefix = "";
		if (strip) {
			// Remove one shared top-level folder, e.g. "MyPack/data/..."
			const first = names.length ? names[0][0].split("/")[0] : "";
			if (first && names.every(([n]) => n.startsWith(first + "/")) && !["data", "chars", "stages", "font", "sound"].includes(first.toLowerCase()))
				prefix = first + "/";
		}
		for (const [n, d] of names) ikemenFS.writeFile("/" + n.slice(prefix.length), d);
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
			for (const f of $("extra").files) {
				status("Unpacking " + f.name + "...");
				await load(await f.arrayBuffer(), true);
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
	$("start").addEventListener("click", start);
	$("fs").addEventListener("click", () => $("stage").requestFullscreen && $("stage").requestFullscreen());
	$("errclose").addEventListener("click", () => { $("overlay").hidden = true; });
	window.addEventListener("keydown", e => {
		if (!$("stage").hidden && (e.code.startsWith("Arrow") || e.code === "Space" || e.code === "Tab")) e.preventDefault();
	}, true);
})();
