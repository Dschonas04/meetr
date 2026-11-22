// Meetr im Browser: WebRTC-Mesh, Signalisierung über WebSocket. Wer neu kommt,
// bietet an, mit je einem Platz für Ton und Bild. Getauscht wird danach nur
// noch die Spur, das geht auch, wenn jemand ohne Kamera gekommen ist.
(() => {
  const $ = (id) => document.getElementById(id);
  const raum = $("raum");
  if (!raum) return;
  const kennung = raum.dataset.kennung;
  const angemeldet = raum.dataset.angemeldet === "true";
  const gastgeber = raum.dataset.gastgeber === "true";
  let ice = [];
  let lokal = new MediaStream(), bildschirm = null, ws = null, meineID = "", beendet = false;
  let gesperrt = false, sprecher = "", gepinnt = "", kette = Promise.resolve(), laufend = 0;
  const peers = new Map();  // id -> {pc, anbieter}
  const leute = new Map();  // id -> Person
  const kacheln = new Map();
  const meiner = { mikro: false, kamera: false, bild: false, hand: false };

  // ---------- Vorraum
  const anhalten = (strom) => strom?.getTracks().forEach((t) => t.stop());
  const medien = async (video, audio) => {
    if (!video && !audio) return new MediaStream();
    return navigator.mediaDevices.getUserMedia({ video, audio });
  };
  const vorschauStarten = async () => {
    const nr = ++laufend;
    const video = $("mitkamera").checked, audio = $("mitmikro").checked;
    $("beitreten").disabled = true;
    $("vorhinweis").textContent = "";
    anhalten(lokal);
    let neu;
    try {
      neu = await medien(video, audio);
    } catch (e) {
      // Fehlt eins von beiden, geht das andere trotzdem.
      const l = [];
      if (video && audio) {
        for (const p of [[false, true], [true, false]]) {
          try { l.push(...(await medien(...p)).getTracks()); } catch { /* steht im Hinweis */ }
        }
      }
      neu = new MediaStream(l);
      if (nr === laufend) $("vorhinweis").textContent = "Kamera oder Mikrofon nicht verfügbar. Beitreten geht trotzdem.";
    }
    if (nr !== laufend) { anhalten(neu); return; }
    lokal = neu;
    $("vorschau").srcObject = lokal;
    $("beitreten").disabled = false;
  };
  $("mitkamera").addEventListener("change", vorschauStarten);
  $("mitmikro").addEventListener("change", vorschauStarten);
  vorschauStarten();

  const senden = (n) => { if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(n)); };

  // ---------- Bühne
  const anordnen = () => {
    const n = kacheln.size || 1, b = $("buehne");
    b.style.gridTemplateColumns = `repeat(${Math.ceil(Math.sqrt(n))}, minmax(0, 1fr))`;
    b.style.setProperty("--kleine", Math.max(1, n - 1));
    const an = b.classList.contains("sprecher");
    const teilt = [...leute].find(([, p]) => p.zustand.bild)?.[0];
    const andere = [...kacheln.keys()].find((id) => id !== meineID) || meineID;
    const gross = [gepinnt, teilt, sprecher, andere].find((id) => id && kacheln.has(id));
    for (const [id, k] of kacheln) k.classList.toggle("gross", an && id === gross);
  };
  const kachel = (id) => {
    const k = document.createElement("div");
    k.className = "kachel" + (id === meineID ? " ich" : "");
    k.dataset.id = id;
    const v = document.createElement("video");
    v.autoplay = true; v.playsInline = true; v.muted = id === meineID;
    v.srcObject = id === meineID ? lokal : new MediaStream();
    const teile = [v, ...["ohnebild", "schild", "marke-hand"].map((c) => {
      const d = document.createElement("div"); d.className = c; return d;
    })];
    k.append(...teile);
    k.addEventListener("click", () => { gepinnt = gepinnt === id ? "" : id; anordnen(); });
    $("buehne").append(k);
    kacheln.set(id, k);
  };
  const zeigen = (id) => {
    const p = leute.get(id), k = kacheln.get(id);
    if (!p || !k) return;
    const ich = id === meineID, z = p.zustand;
    k.querySelector(".schild").textContent = (ich ? "Du" : p.name) + (z.mikro ? "" : " · stumm");
    k.querySelector(".ohnebild").textContent = [...p.name][0]?.toUpperCase() || "";
    k.querySelector(".marke-hand").textContent = z.hand ? "✋" : "";
    k.classList.toggle("ohnevideo", !z.kamera);
    k.classList.toggle("teilen", z.bild);
  };
  const leuteZeigen = () => {
    const mich = leute.get(meineID);
    $("leute").replaceChildren(...[...leute].map(([id, p]) => {
      const li = document.createElement("li");
      const s = document.createElement("span");
      s.textContent = (id === meineID ? "Du" : p.name) + (p.zustand.hand ? " ✋" : "");
      const rolle = document.createElement("span");
      rolle.className = "rolle";
      rolle.textContent = [p.gastgeber ? "Gastgeber" : "", p.zustand.mikro ? "" : "stumm"].filter(Boolean).join(" · ");
      li.append(s, rolle);
      if (id !== meineID && mich && (mich.gastgeber || (mich.angemeldet && !p.angemeldet))) {
        for (const [typ, text] of [["stumm", "Stumm"], ["kamera-aus", "Kamera aus"], ["entfernen", "Entfernen"]]) {
          const b = document.createElement("button");
          b.type = "button"; b.textContent = text;
          b.onclick = () => senden({ typ, an: id });
          li.append(b);
        }
      }
      return li;
    }));
  };
  const alles = () => { for (const id of leute.keys()) zeigen(id); leuteZeigen(); anordnen(); };
  const meldung = (text, wer) => {
    const d = document.createElement("div");
    if (wer) {
      const b = document.createElement("b");
      b.textContent = wer + ": ";
      d.append(b);
    } else d.className = "meldung";
    d.append(text);
    $("chat").append(d);
    $("chat").scrollTop = 1e9;
  };

  // ---------- Verbindungen
  const spur = (art) => (art === "audio" ? lokal.getAudioTracks()[0]
    : bildschirm?.getVideoTracks()[0] || lokal.getVideoTracks()[0]) || null;

  const verbinden = (id, anbieter) => {
    const pc = new RTCPeerConnection({ iceServers: ice, bundlePolicy: "max-bundle" });
    peers.set(id, { pc, anbieter });
    pc.onicecandidate = (e) => { if (e.candidate) senden({ typ: "signal", an: id, daten: { kandidat: e.candidate } }); };
    pc.ontrack = (e) => {
      const s = kacheln.get(id)?.querySelector("video").srcObject;
      if (s && !s.getTracks().includes(e.track)) s.addTrack(e.track);
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState !== "failed") return;
      if (anbieter) pc.restartIce();
      else meldung("Die Verbindung zu " + (leute.get(id)?.name || "jemandem") + " hakt.");
    };
    if (anbieter) {
      pc.onnegotiationneeded = async () => {
        try {
          await pc.setLocalDescription(await pc.createOffer());
          senden({ typ: "signal", an: id, daten: { sdp: pc.localDescription } });
        } catch (e) { console.error("Angebot", e); }
      };
      for (const art of ["audio", "video"]) pc.addTransceiver(spur(art) || art, { direction: "sendrecv" });
    }
    return peers.get(id);
  };

  const signal = async (von, d) => {
    if (!d || !leute.has(von)) return;
    const { pc, anbieter } = peers.get(von) || verbinden(von, false);
    if (d.sdp?.type === "offer" && !anbieter) {
      await pc.setRemoteDescription(d.sdp);
      for (const t of pc.getTransceivers()) {
        t.direction = "sendrecv";
        await t.sender.replaceTrack(spur(t.receiver.track.kind));
      }
      await pc.setLocalDescription(await pc.createAnswer());
      senden({ typ: "signal", an: von, daten: { sdp: pc.localDescription } });
    } else if (d.sdp?.type === "answer" && anbieter) {
      await pc.setRemoteDescription(d.sdp);
    } else if (d.kandidat) {
      await pc.addIceCandidate(d.kandidat);
    }
  };

  const dazu = (p) => { leute.set(p.id, p); kachel(p.id); };
  const trennen = (id) => {
    peers.get(id)?.pc.close();
    peers.delete(id);
    kacheln.get(id)?.remove();
    kacheln.delete(id);
    leute.delete(id);
    if (gepinnt === id) gepinnt = "";
    alles();
  };

  const melden = () => {
    meiner.mikro = !!lokal.getAudioTracks()[0]?.enabled;
    meiner.kamera = !!lokal.getVideoTracks()[0]?.enabled;
    meiner.bild = !!bildschirm;
    const mich = leute.get(meineID);
    if (!mich) return;
    mich.zustand = { ...meiner };
    $("mikro").classList.toggle("aus", !meiner.mikro);
    $("kamera").classList.toggle("aus", !meiner.kamera);
    $("bildschirm").classList.toggle("an", meiner.bild);
    $("hand").setAttribute("aria-pressed", String(meiner.hand));
    senden({ typ: "zustand", daten: meiner });
    alles();
  };

  const schluss = (text) => {
    if (beendet) return;
    beendet = true;
    for (const { pc } of peers.values()) pc.close();
    anhalten(lokal); anhalten(bildschirm);
    for (const id of ["vorraum", "warten", "konferenz"]) $(id).hidden = true;
    $("ende").hidden = false;
    $("endetext").textContent = text;
    ws?.close();
  };
  const gruende = {
    "voll": "Der Raum ist voll.",
    "gesperrt": "Der Raum ist abgeschlossen.",
    "zu-oft": "Zu viele Versuche, bitte kurz warten.",
    "weg-raum": "Diesen Raum gibt es nicht mehr.",
    "anmelden": "Dieser Raum braucht eine Anmeldung.",
    "abgelehnt": "Du wurdest nicht hereingelassen.",
    "entfernt": "Du wurdest aus dem Raum entfernt.",
  };

  const empfangen = async (n) => {
    if (gruende[n.typ]) return schluss(gruende[n.typ]);
    switch (n.typ) {
      case "kennwort":
        ws.onclose = null; ws.close(); ws = null;
        $("beitreten").disabled = false;
        $("vorhinweis").textContent = "Das Kennwort stimmt nicht.";
        $("kennwort")?.focus();
        break;
      case "warten":
        $("vorraum").hidden = true; $("warten").hidden = false;
        break;
      case "willkommen":
        meineID = n.von; ice = n.ice || []; gesperrt = !!n.ja;
        $("vorraum").hidden = $("warten").hidden = true;
        $("konferenz").hidden = false;
        if (matchMedia("(max-width: 760px)").matches) $("konferenz").classList.add("ohne-seite");
        for (const p of n.liste || []) {
          dazu(p);
          if (p.id !== meineID) verbinden(p.id, true);
        }
        schlossZeigen();
        melden();
        break;
      case "neu":
        dazu(n.wer); alles();
        meldung(n.wer.name + " ist da");
        break;
      case "weg":
        if (!leute.has(n.von)) break;
        meldung(leute.get(n.von).name + " ist gegangen");
        trennen(n.von);
        break;
      case "signal": await signal(n.von, n.daten); break;
      case "zustand": {
        const p = leute.get(n.von);
        if (!p) break;
        if (n.wer.zustand.hand && !p.zustand.hand) meldung(p.name + " hebt die Hand");
        p.zustand = n.wer.zustand;
        alles();
        break;
      }
      case "reaktion": {
        const k = kacheln.get(n.von);
        if (!k) break;
        k.querySelector(".reaktion")?.remove();
        const d = document.createElement("div");
        d.className = "reaktion"; d.textContent = n.text;
        k.append(d);
        setTimeout(() => d.remove(), 2500);
        break;
      }
      case "chat": meldung(n.text, n.von === meineID ? "Du" : n.name); break;
      case "anfrage": {
        const d = document.createElement("div");
        d.className = "karte"; d.dataset.id = n.von;
        d.append(n.name + " möchte herein ");
        for (const [typ, text] of [["zulassen", "Zulassen"], ["ablehnen", "Ablehnen"]]) {
          const b = document.createElement("button");
          b.type = "button"; b.textContent = text;
          b.onclick = () => senden({ typ, an: n.von });
          d.append(b);
        }
        $("anfragen").append(d);
        $("konferenz").classList.remove("ohne-seite");
        break;
      }
      case "erledigt": $("anfragen").querySelector(`[data-id="${CSS.escape(n.von)}"]`)?.remove(); break;
      case "kein-platz": meldung("Der Raum ist voll."); break;
      case "abschliessen":
        gesperrt = !!n.ja; schlossZeigen();
        meldung(gesperrt ? "Der Raum ist abgeschlossen." : "Der Raum ist wieder offen.");
        break;
      case "stumm":
        lokal.getAudioTracks().forEach((t) => { t.enabled = false; });
        meldung(n.name + " hat dich stummgeschaltet"); melden();
        break;
      case "kamera-aus":
        lokal.getVideoTracks().forEach((t) => { t.enabled = false; });
        meldung(n.name + " hat deine Kamera ausgeschaltet"); melden();
        break;
    }
  };

  $("beitritt").addEventListener("submit", (e) => {
    e.preventDefault();
    if (ws) return;
    $("beitreten").disabled = true;
    $("vorhinweis").textContent = "";
    const hallo = { typ: "hallo", name: $("name")?.value || "", text: $("kennwort")?.value || "" };
    ws = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws/${kennung}`);
    ws.onopen = () => senden(hallo);
    // Der Reihe nach: ein Kandidat darf seine Beschreibung nicht überholen.
    ws.onmessage = (ev) => {
      kette = kette.then(() => empfangen(JSON.parse(ev.data))).catch((err) => console.error("Meetr", err));
    };
    ws.onclose = () => { kette = kette.then(() => { if (ws) schluss("Die Verbindung ist getrennt."); }); };
  });

  // ---------- Steuerung
  const umschalten = (art) => {
    for (const t of art === "audio" ? lokal.getAudioTracks() : lokal.getVideoTracks()) t.enabled = !t.enabled;
    melden();
  };
  $("mikro").onclick = () => umschalten("audio");
  $("kamera").onclick = () => umschalten("video");
  const bildTauschen = async () => {
    const t = spur("video");
    for (const { pc } of peers.values()) {
      const tr = pc.getTransceivers().find((x) => x.receiver.track.kind === "video");
      if (tr) await tr.sender.replaceTrack(t).catch((e) => console.error("Bild", e));
    }
    const v = kacheln.get(meineID)?.querySelector("video");
    if (v) v.srcObject = bildschirm || lokal;
    melden();
  };
  const teilenEnde = () => {
    if (!bildschirm) return;
    anhalten(bildschirm);
    bildschirm = null;
    return bildTauschen();
  };
  $("bildschirm").onclick = async () => {
    if (bildschirm) return teilenEnde();
    try {
      bildschirm = await navigator.mediaDevices.getDisplayMedia({ video: true });
    } catch {
      return; // abgebrochen
    }
    bildschirm.getVideoTracks()[0].onended = teilenEnde;
    await bildTauschen();
  };
  $("hand").onclick = () => { meiner.hand = !meiner.hand; melden(); };
  for (const b of document.querySelectorAll("[data-reaktion]")) b.onclick = () => senden({ typ: "reaktion", text: b.dataset.reaktion });
  $("ansicht").onclick = () => {
    const an = $("buehne").classList.toggle("sprecher");
    $("ansicht").setAttribute("aria-pressed", String(an));
    anordnen();
  };
  function schlossZeigen() {
    const s = $("schloss");
    if (!s) return;
    s.setAttribute("aria-pressed", String(gesperrt));
    s.textContent = gesperrt ? "Aufschließen" : "Abschließen";
  }
  if (gastgeber && angemeldet) $("schloss").onclick = () => senden({ typ: "abschliessen", ja: !gesperrt });
  $("seiteknopf").onclick = () => $("konferenz").classList.toggle("ohne-seite");
  $("gehen").onclick = () => {
    beendet = true;
    anhalten(lokal); anhalten(bildschirm);
    ws?.close();
    location.href = angemeldet ? "/" : "/r/" + kennung;
  };
  $("nochmal").onclick = () => location.reload();
  $("chatform").onsubmit = (e) => {
    e.preventDefault();
    const t = $("chattext").value.trim();
    if (t) { senden({ typ: "chat", text: t }); $("chattext").value = ""; }
  };
  document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.repeat || e.target.closest?.("input, textarea") || $("konferenz").hidden) return;
    const ziel = { m: "mikro", v: "kamera", h: "hand", s: "ansicht" }[e.key.toLowerCase()];
    if (ziel) $(ziel).click();
  });

  // Wer spricht, sagt der Empfänger selbst.
  setInterval(() => {
    if (!peers.size) return;
    let lauteste = "", pegel = 0.02;
    for (const [id, { pc }] of peers) {
      let l = 0;
      for (const r of pc.getReceivers()) {
        if (r.track.kind !== "audio" || !r.getSynchronizationSources) continue;
        for (const q of r.getSynchronizationSources()) l = Math.max(l, q.audioLevel || 0);
      }
      kacheln.get(id)?.classList.toggle("spricht", l > 0.02);
      if (l > pegel) { pegel = l; lauteste = id; }
    }
    if (lauteste && lauteste !== sprecher) { sprecher = lauteste; anordnen(); }
  }, 700);
})();
