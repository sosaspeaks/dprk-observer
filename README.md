# dprk-observer
Telemetry probe, reachability monitor, and archival engine for DPRK sovereign web infrastructure.

### Current Findings

* **Server Environment**: Web servers run Apache 2.4.25 hosted on **RedStar4.0** with OpenSSL 1.0.1e-fips.
* **Edge & WAF Defenses**:
  * Edge gateways enforce aggressive rate limits and connection policing, terminating connections immediately with TCP resets (`EOF` / `curl: (52) Empty reply`) when automated request profiles or rapid polling bursts are detected.
  * Triggered rate-limit penalties incur a 3-to-10 minute IP-level ban on the upstream relay/exit node.
  * Static file requests containing unnormalized relative traversal sequences (e.g., `/../../..`) trigger web application firewall rules, responding with immediate `403 Forbidden` statuses or dropped sockets.
* **Endpoint Route Discrepancies**:
  * `/detail_com/comde/`: Standard news, economic dispatches, and cultural reporting; renders conventional HTML and predictably links to static assets.
  * `/revo_de/`: Revolutionary activities and Supreme Leadership coverage; enforces stricter session validations and yields significantly higher connection reset rates against automated HTTP clients.

---

### Multimedia & Video Streaming (HLS)

* **Player Integration Architecture**:
  * Multimedia dispatch pages (`/detail_com/vi_video/<id>`) do not embed raw `<video>` or `<audio>` media directly.
  * Content is delivered inside an `<iframe>` player endpoint located at `http://vok.rep.kp/hls/player/<id>`.
  * The embedded player runs a customized instance of **Video.js** that includes domestic theme stylesheets (`pyb.css`) and localization packages (`lang/kp.js`).
* **HLS Manifest & Short-Lived Tokens**:
  * Media delivery relies on HTTP Live Streaming (HLS), using master `.m3u8` manifests delivering chunked `.ts` segment streams.
  * Manifest URLs require an active authentication query parameter (`?token=...`).
  * The Base64 token payload contains dynamic verification keys, including an MD5 salt, the internal media ID, a creation timestamp, and an explicit expiry window:
    ```json
    {
      "salt": "5e4e538c65003",
      "tokenTimeOut": "300",
      "filename": "<media_id>",
      "time": 1791432412
    }
    ```
  * Tokens expire after 300 seconds (5 minutes). Automated extraction workflows must parse the live player HTML immediately prior to stream ingestion.
* **Static Visual Media Assets**:
  * Full-frame preview posters are stored statically alongside media IDs: `http://vok.rep.kp/hls/videos/cbc_<id>/<id>.jpg`.
  * Video scrub grids (spritesheets) containing complete timeline thumbnail matrices (e.g., $1500 \times 840$ pixel sheets) are available at: `http://vok.rep.kp/hls/videos/cbc_<id>/<id>_thumbsSprit.jpg`.
