// Meetr im Browser: WebRTC-Mesh, Signalisierung über WebSocket.
(() => {
  const $ = (id) => document.getElementById(id);
  const raum = $("raum");
  if (!raum) return;
  const kennung = raum.dataset.kennung;
  const angemeldet = raum.dataset.angemeldet === "true";
  let ice = [];
  try { ice = JSON.parse($("ice").textContent || "[]"); } catch {}
  let lokal = null, bildschirm = null, ws = null, meineID = "";
  const peers = new Map(); // id -> {pc, kachel, name}
  const namen = new Map();

  const vorschauStarten = async () => {
    try {
      lokal = await navigator.mediaDevices.getUserMedia({ video: $("mitkamera").checked, audio: $("mitmikro").checked });
      $("vorschau").srcObject = lokal;
    } catch (e) {
      lokal = new MediaStream();
      $("vorhinweis").textContent = "Kamera oder Mikrofon nicht verfügbar: " + e.message;
    }
  };
  $("mitkamera").addEventListener("change", vorschauStarten);
  $("mitmikro").addEventListener("change", vorschauStarten);
  vorschauStarten();

  const kachel = (id, name, ich) => {
    const k = document.createElement("div");
    k.className = "kachel" + (ich ? " ich" : "");
    k.dataset.id = id;
    const v = document.createElement("video");
    v.autoplay = true; v.playsInline = true; if (ich) v.muted = true;
    const s = document.createElement("div");
    s.className = "schild"; s.textContent = name + (ich ? " (du)" : "");
    k.append(v, s);
    $("buehne").append(k);
    anordnen();
    return k;
  };
  const anordnen = () => {
    const n = $("buehne").children.length || 1;
    const sp = Math.ceil(Math.sqrt(n));
    $("buehne").style.gridTemplateColumns = `repeat(${sp}, 1fr)`;
  };
  const leuteZeigen = () => {
    const ul = $("leute"); ul.innerHTML = "";
    const eintrag = (id, name) => {
      const li = document.createElement("li");
      const s = document.createElement("span"); s.textContent = name; li.append(s);
      if (angemeldet && id !== meineID) {
        for (const [typ, text] of [["stumm", "Stumm"], ["entfernen", "Raus"]]) {
          const b = document.createElement("button"); b.textContent = text;
          b.onclick = () => senden({ typ, an: id }); li.append(b);
        }
      }
      ul.append(li);
    };
    eintrag(meineID, "Du");
    for (const [id, n] of namen) eintrag(id, n);
  };
  const chatZeile = (name, text) => {
    const d = document.createElement("div");
    const b = document.createElement("b"); b.textContent = name + ": ";
    d.append(b, document.createTextNode(text));
    $("chat").append(d); $("chat").scrollTop = 1e9;
  };
  const senden = (n) => ws && ws.readyState === 1 && ws.send(JSON.stringify(n));

  const verbinden = (id, name, anbieten) => {
    const pc = new RTCPeerConnection({ iceServers: ice });
    const k = kachel(id, name, false);
    peers.set(id, { pc, kachel: k });
    namen.set(id, name); leuteZeigen();
    const strom = bildschirm || lokal;
    for (const t of lokal.getAudioTracks()) pc.addTrack(t, lokal);
    const vt = (bildschirm && bildschirm.getVideoTracks()[0]) || lokal.getVideoTracks()[0];
    if (vt) pc.addTrack(vt, strom);
    pc.onicecandidate = (e) => e.candidate && senden({ typ: "signal", an: id, daten: { kandidat: e.candidate } });
    pc.ontrack = (e) => { k.querySelector("video").srcObject = e.streams[0]; };
    pc.onconnectionstatechange = () => { if (pc.connectionState === "failed") pc.restartIce(); };
    if (anbieten) {
      pc.onnegotiationneeded = async () => {
        await pc.setLocalDescription(await pc.createOffer());
        senden({ typ: "signal", an: id, daten: { sdp: pc.localDescription } });
      };
    }
    return pc;
  };
  const trennen = (id) => {
    const p = peers.get(id);
    if (p) { p.pc.close(); p.kachel.remove(); peers.delete(id); }
    namen.delete(id); leuteZeigen(); anordnen();
  };

  const signal = async (von, d) => {
    let p = peers.get(von);
    if (!p) { verbinden(von, namen.get(von) || "…", false); p = peers.get(von); }
    if (d.sdp) {
      await p.pc.setRemoteDescription(d.sdp);
      if (d.sdp.type === "offer") {
        await p.pc.setLocalDescription(await p.pc.createAnswer());
        senden({ typ: "signal", an: von, daten: { sdp: p.pc.localDescription } });
      }
    } else if (d.kandidat) {
      try { await p.pc.addIceCandidate(d.kandidat); } catch {}
    }
  };

  $("beitreten").onclick = () => {
    const name = $("name") ? $("name").value.trim() : "";
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    ws = new WebSocket(`${proto}//${location.host}/ws/${kennung}?name=${encodeURIComponent(name)}`);
    $("vorraum").hidden = true;
    ws.onmessage = async (ev) => {
      const n = JSON.parse(ev.data);
      switch (n.typ) {
        case "warten": $("warten").hidden = false; break;
        case "willkommen":
          meineID = n.von; $("warten").hidden = true; $("konferenz").hidden = false;
          kachel(meineID, "Du", true).querySelector("video").srcObject = lokal;
          for (const p of n.liste || []) if (p.id !== meineID) { namen.set(p.id, p.name); verbinden(p.id, p.name, true); }
          leuteZeigen(); break;
        case "neu": namen.set(n.von, n.name); leuteZeigen(); chatZeile("Meetr", n.name + " ist da"); break;
        case "weg": chatZeile("Meetr", (namen.get(n.von) || "Jemand") + " ist gegangen"); trennen(n.von); break;
        case "signal": await signal(n.von, n.daten); break;
        case "chat": chatZeile(n.name, n.text); break;
        case "anfrage": {
          const d = document.createElement("div"); d.className = "karte"; d.dataset.id = n.von;
          d.append(document.createTextNode(n.name + " möchte herein "));
          for (const [typ, text] of [["zulassen", "Zulassen"], ["ablehnen", "Ablehnen"]]) {
            const b = document.createElement("button"); b.textContent = text;
            b.onclick = () => { senden({ typ, an: n.von }); d.remove(); }; d.append(b);
          }
          $("anfragen").append(d); break;
        }
        case "erledigt": $("anfragen").querySelector(`[data-id="${n.von}"]`)?.remove(); break;
        case "stumm": lokal.getAudioTracks().forEach((t) => (t.enabled = false)); $("mikro").classList.add("aus"); chatZeile("Meetr", n.name + " hat dich stummgeschaltet"); break;
        case "zustand": if (n.daten?.hand) chatZeile("Meetr", (namen.get(n.von) || "?") + " hebt die Hand ✋"); break;
        case "abgelehnt": case "entfernt": case "voll":
          alert(n.typ === "voll" ? "Der Raum ist voll." : n.typ === "abgelehnt" ? "Du wurdest nicht hereingelassen." : "Du wurdest aus dem Raum entfernt.");
          location.href = "/r/" + kennung; break;
      }
    };
    ws.onclose = () => { if (!$("konferenz").hidden) chatZeile("Meetr", "Verbindung getrennt"); };
  };

  const umschalten = (art, knopf) => {
    const tr = art === "audio" ? lokal.getAudioTracks() : lokal.getVideoTracks();
    tr.forEach((t) => (t.enabled = !t.enabled));
    knopf.classList.toggle("aus", tr.length > 0 && !tr[0].enabled);
  };
  $("mikro").onclick = () => umschalten("audio", $("mikro"));
  $("kamera").onclick = () => umschalten("video", $("kamera"));
  const videoErsetzen = (track) => {
    for (const { pc } of peers.values()) {
      const s = pc.getSenders().find((x) => x.track && x.track.kind === "video");
      if (s) s.replaceTrack(track);
    }
  };
  $("bildschirm").onclick = async () => {
    if (bildschirm) {
      bildschirm.getTracks().forEach((t) => t.stop()); bildschirm = null;
      videoErsetzen(lokal.getVideoTracks()[0] || null); $("bildschirm").classList.remove("an"); return;
    }
    try {
      bildschirm = await navigator.mediaDevices.getDisplayMedia({ video: true });
      const t = bildschirm.getVideoTracks()[0];
      videoErsetzen(t); $("bildschirm").classList.add("an");
      t.onended = () => $("bildschirm").onclick();
    } catch {}
  };
  $("hand").onclick = () => { senden({ typ: "zustand", daten: { hand: true } }); chatZeile("Du", "hebst die Hand ✋"); };
  $("seiteknopf").onclick = () => $("konferenz").classList.toggle("ohne-seite");
  $("gehen").onclick = () => { ws && ws.close(); location.href = angemeldet ? "/" : "/r/" + kennung; };
  $("chatform").onsubmit = (e) => {
    e.preventDefault();
    const t = $("chattext").value.trim();
    if (t) { senden({ typ: "chat", text: t }); $("chattext").value = ""; }
  };
  document.addEventListener("keydown", (e) => {
    if (e.target.tagName === "INPUT" || $("konferenz").hidden) return;
    if (e.key === "m") $("mikro").click();
    if (e.key === "v") $("kamera").click();
  });
})();
