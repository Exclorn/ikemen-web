// Finds characters and stages in uploaded files and adds them to data/select.def.
(function () {
	const dec = new TextDecoder("utf-8");
	const text = d => dec.decode(d);

	// files: array of [path, bytes] already stripped of any wrapper folder, e.g. ["chars/Ryu_Folder/evil_ryu.def", bytes]
	// Returns { chars: ["Ryu_Folder/evil_ryu.def"], stages: ["stages/foo.def"] }
	function scan(files) {
		const chars = [], stages = [];
		for (const [p, d] of files) {
			const parts = p.split("/");
			if (!/\.def$/i.test(p)) continue;
			const top = parts[0].toLowerCase();
			if (top === "chars" && parts.length >= 3) {
				// A character file has a [Files] section with a sprite entry. Skips storyboards, etc.
				const t = text(d);
				if (/^\s*\[files\]/im.test(t) && /^\s*sprite\s*=/im.test(t)) chars.push(parts.slice(1).join("/"));
			} else if (top === "stages" && parts.length >= 2) {
				if (/^\s*\[stageinfo\]/im.test(text(d))) stages.push(p);
			}
		}
		return { chars, stages };
	}

	// Inserts the entries into the text of select.def and returns the new text
	function apply(src, chars, stages) {
		const nl = src.includes("\r\n") ? "\r\n" : "\n";
		const lines = src.split(/\r?\n/);
		const header = name => lines.findIndex(l => l.trim().toLowerCase() === "[" + name.toLowerCase() + "]");
		const sectionEnd = start => {
			let e = lines.length;
			for (let i = start + 1; i < lines.length; i++) if (/^\s*\[/.test(lines[i])) { e = i; break; }
			return e;
		};
		const have = new Set(lines.map(l => l.split(",")[0].trim().toLowerCase()));
		const insertAt = (name, entries, beforeRegex) => {
			entries = entries.filter(e => !have.has(e.toLowerCase()));
			if (!entries.length) return;
			let h = header(name);
			if (h < 0) { lines.push("[" + name + "]"); h = lines.length - 1; }
			const end = sectionEnd(h);
			let at = end;
			if (beforeRegex) {
				for (let i = h + 1; i < end; i++) if (beforeRegex.test(lines[i])) { at = i; break; }
			}
			if (at === end) while (at > h + 1 && lines[at - 1].trim() === "") at--;
			lines.splice(at, 0, ...entries);
		};
		// Process the later section first so earlier line numbers stay valid
		const order = header("characters") < header("extrastages") ? ["stages", "chars"] : ["chars", "stages"];
		for (const k of order) {
			if (k === "chars") insertAt("Characters", chars, /^\s*randomselect\b/i);
			else insertAt("ExtraStages", stages);
		}
		return lines.join(nl);
	}

	globalThis.ikemenSelectDef = { scan, apply };
})();
