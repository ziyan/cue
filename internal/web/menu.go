package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"image/png"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/util/picture"
	"github.com/ziyan/cue/internal/version"
	"github.com/ziyan/cue/internal/wallpaper"
)

// The menu somebody at the screen can open.
//
// It is what the floating mark opens, and it is a page of this daemon's own so
// that it can act: a page served from somewhere else may not, whatever it is
// displayed on. See fromOurOwnPage.
//
// It offers actions and no settings. Somebody standing at a screen with a
// mouse wants to restart something, skip an item, or start the wireless setup
// again; changing a URL or a timezone is work for a keyboard and the web
// interface. Restricting it to actions also means nothing here can leave the
// device in a state somebody has to undo.

// menu renders the page shown inside the overlay.
func (self *Server) menu(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()

	addresses := make([]string, 0, 3)
	for _, address := range machineAddresses() {
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		addresses = append(addresses, "no address")
	}

	network := ""
	if state := self.device.Network(); state != nil {
		if status := state.State(); len(status.Interfaces) > 0 {
			for _, one := range status.Interfaces {
				if one.Wireless != nil && one.Wireless.SSID != "" {
					network = one.Wireless.SSID
					break
				}
			}
		}
	}

	_, setUp := self.device.SetupNetwork()

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := menuTemplate.Execute(response, map[string]interface{}{
		"Device":     configuration.Device.Name,
		"Identifier": configuration.Device.Identifier,
		"Version":    version.String(),
		"Addresses":  strings.Join(addresses, "  ·  "),
		"Network":    network,
		"Uptime":     time.Since(self.device.StartedAt()).Round(time.Second).String(),
		"Machine":    runtime.GOARCH,
		"SettingUp":  setUp,
		"Mark":       template.URL("data:image/png;base64," + smallMark()),
	}); err != nil {
		log.Debugf("cannot render the menu: %s", err)
	}
}

// holdPlaylist keeps the screen still while the menu is open, and lets it go
// again when it closes.
func (self *Server) holdPlaylist(response http.ResponseWriter, request *http.Request) {
	browser := self.device.Browser()
	if browser == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no browser to hold")
		return
	}
	if strings.HasSuffix(request.URL.Path, "/release") {
		browser.Release()
	} else {
		browser.Hold()
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"held": true})
}

// smallMark is this project's mark, shrunk to something a button wants and
// encoded for putting straight into a page.
//
// Inline rather than fetched, because the mark has to appear on pages served
// by other people, and a page that fetches it from this device is a page
// making a cross-origin request that its own rules may forbid. Encoded once:
// it is the same bytes every time.
var smallMark = sync.OnceValue(func() string {
	mark := wallpaper.Mark()
	if mark == nil {
		return ""
	}
	small := picture.Shrink(mark, 96)

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, small); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
})

var menuTemplate = template.Must(template.New("menu").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Device }}</title>
<style>
  :root {
    color-scheme: dark; --accent: #f57915;
    /* One spacing scale. Every gap and margin below is a multiple of it, so
       that nothing ends up 7 pixels from one thing and 14 from another --
       which is what the first version did, and it read as carelessness. */
    --step: 1.2vmin;
  }
  * { box-sizing: border-box; }
  /* Any display rule of ours beats the browser's own "hidden means none", and
     .actions sets display:grid -- so hiding the actions to show the network
     section did nothing at all, and both were on screen at once. */
  [hidden] { display: none !important; }
  html, body { margin: 0; height: 100%; background: rgba(6,8,10,0.72);
    font: 2vmin system-ui, -apple-system, "Segoe UI", Roboto,
      "Noto Sans CJK SC", "Noto Sans CJK JP", sans-serif; color: #e7ecf3; }
  body { display: grid; place-items: center; padding: calc(var(--step) * 2); }

  /* The panel is allowed to be shorter than its contents and scroll. It used
     not to be, and the wired form ran 113 pixels past the bottom of a 673
     pixel screen, taking Apply and Back with it. */
  /* Spaced by gap, not by margins between siblings.
     Margins were tried and were wrong twice over. "This block, then a margin
     before the next" breaks the moment something hidden sits between the two
     -- and something hidden always does here, because the confirmation and
     the progress line live between the actions and the close button. A flex
     column with a gap simply skips what is not displayed. */
  .panel { width: min(64vmin, 92vw); max-height: calc(100vh - var(--step) * 4);
    overflow-y: auto; background: #0f1216; border: 1px solid #2a323d;
    border-radius: calc(var(--step) * 1.4); padding: calc(var(--step) * 2.4);
    box-shadow: 0 2vmin 6vmin rgba(0,0,0,0.6);
    display: flex; flex-direction: column; gap: calc(var(--step) * 1.6); }
  #network, #wireless, #wired, #confirm {
    display: flex; flex-direction: column; gap: calc(var(--step) * 1.6); }

  header { display: flex; align-items: center; gap: calc(var(--step) * 1.4); }
  header img { width: 6vmin; height: 6vmin; flex: none; }
  h1 { font-size: 2.8vmin; margin: 0; flex: 1; min-width: 0;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  /* The languages, small and out of the way until wanted. */
  .languages { display: flex; gap: calc(var(--step) * 0.5); flex: none; }
  .languages button { padding: calc(var(--step) * 0.5) var(--step);
    font-size: 1.6vmin; color: #9fb0c5; background: #131920; }
  .languages button.on { color: #e7ecf3; border-color: var(--accent); }

  .facts { color: #9fb0c5; font-size: 1.7vmin; line-height: 1.7; margin: 0; }
  .facts b { color: #e7ecf3; font-weight: 600; }

  .actions { display: grid; gap: var(--step); }

  button { all: unset; box-sizing: border-box; cursor: pointer;
    padding: calc(var(--step) * 1.2) calc(var(--step) * 1.5);
    border-radius: var(--step); background: #171d24; border: 1px solid #2a323d;
    text-align: left; }
  button:hover { background: #1f2731; }
  button:focus-visible { border-color: var(--accent); }
  button .what { display: block; }
  button .why { display: block; color: #9fb0c5; font-size: 1.5vmin;
    margin-top: calc(var(--step) * 0.3); }
  button.danger .what { color: #ffc9d1; }
  /* Going back is not one of the things on offer; it should not look like one. */
  button.quiet { background: none; text-align: center; color: #9fb0c5; }
  button.quiet:hover { background: #131920; }

  .tabs { display: flex; gap: var(--step); }
  .tabs button { flex: 1; text-align: center; }
  .tabs button.on { border-color: var(--accent); }

  .list { max-height: 30vmin; overflow-y: auto; overflow-x: hidden;
    border: 1px solid #2a323d; border-radius: var(--step); }
  .list button { width: 100%; border: 0; border-radius: 0; background: #131920;
    display: flex; align-items: center; gap: var(--step); }
  .list button + button { border-top: 1px solid #2a323d; }
  .list button.on { background: #1f2a35; box-shadow: inset 0.3vmin 0 0 var(--accent); }
  .list .ssid { flex: 1; min-width: 0; overflow: hidden;
    text-overflow: ellipsis; white-space: nowrap; }
  .list .lock { color: #9fb0c5; font-size: 1.5vmin; flex: none; }
  /* Drawn rather than written: block characters render at widths the font
     decides, and the strongest network had its last bar clipped. */
  .list .bars { display: inline-flex; align-items: flex-end;
    gap: calc(var(--step) * 0.3); height: 2.4vmin; flex: none; }
  .list .bars i { width: 0.9vmin; background: #3d4756; border-radius: 0.2vmin; }
  .list .bars i.on { background: #7dd3fc; }

  label { display: block; color: #9fb0c5; font-size: 1.6vmin;
    margin: 0 0 calc(var(--step) * 0.5); }
  label { margin-bottom: calc(var(--step) * 0.4); }
  input, select { width: 100%; padding: var(--step); font: inherit;
    color: inherit; background: #131920; border: 1px solid #2a323d;
    border-radius: var(--step); }
  input:focus, select:focus { outline: none; border-color: var(--accent); }

  #working { color: #9fb0c5; margin: 0; }
</style>
</head>
<body>
<div class="panel">
  <header>
    {{ if .Mark }}<img src="{{ .Mark }}" alt="">{{ end }}
    <h1>{{ .Device }}</h1>
    <div class="languages">
      <button data-language="en">EN</button>
      <button data-language="zh">中文</button>
      <button data-language="ja">日本語</button>
    </div>
  </header>

  <p class="facts">
    <b>{{ .Addresses }}</b><br>
    <span data-t="wireless-is"></span> <b id="joined">{{ .Network }}</b><br>
    {{ .Identifier }} · {{ .Version }} · <span data-t="up-for"></span> {{ .Uptime }}
  </p>

  <div class="actions" id="actions">
    <button data-do="next"><span class="what" data-t="next"></span>
      <span class="why" data-t="next-why"></span></button>
    <button data-do="reload"><span class="what" data-t="reload"></span>
      <span class="why" data-t="reload-why"></span></button>
    <button data-do="network"><span class="what" data-t="network"></span>
      <span class="why" data-t="network-why"></span></button>
    <button data-do="restart-browser" class="danger"><span class="what" data-t="restart-browser"></span>
      <span class="why" data-t="restart-browser-why"></span></button>
    <button data-do="restart-display" class="danger"><span class="what" data-t="restart-display"></span>
      <span class="why" data-t="restart-display-why"></span></button>
    {{ if not .SettingUp }}
    <button data-do="wireless" class="danger"><span class="what" data-t="wireless-again"></span>
      <span class="why" data-t="wireless-again-why"></span></button>
    {{ end }}
  </div>

  <div id="network" hidden>
    <div class="tabs">
      <button id="tab-wireless" class="on"><span class="what" data-t="tab-wireless"></span></button>
      <button id="tab-wired"><span class="what" data-t="tab-wired"></span></button>
    </div>

    <div id="wireless">
      <div class="list" id="networks"></div>
      <div id="secret" hidden>
        <label><span data-t="password-for"></span> <b id="chosen"></b></label>
        <input id="passphrase" type="password" autocomplete="off" autocapitalize="none" spellcheck="false">
      </div>
      <div class="actions">
        <button id="join"><span class="what" data-t="join"></span></button>
        <button id="rescan"><span class="what" data-t="look-again"></span></button>
      </div>
    </div>

    <div id="wired" hidden>
      <label data-t="which-interface"></label>
      <select id="wired-interface"></select>
      <label data-t="how-address"></label>
      <select id="wired-method">
        <option value="dhcp" data-t="by-dhcp"></option>
        <option value="static" data-t="by-address"></option>
      </select>
      <div id="fixed" hidden>
        <label data-t="address-label"></label>
        <input id="wired-address" autocomplete="off" spellcheck="false" placeholder="192.0.2.10/24">
        <label data-t="gateway-label"></label>
        <input id="wired-gateway" autocomplete="off" spellcheck="false" placeholder="192.0.2.1">
        <label data-t="dns-label"></label>
        <input id="wired-dns" autocomplete="off" spellcheck="false">
      </div>
      <div class="actions"><button id="apply"><span class="what" data-t="apply"></span></button></div>
    </div>

    <div class="actions"><button id="back" class="quiet"><span class="what" data-t="back"></span></button></div>
  </div>

  <div id="confirm" hidden>
    <p class="facts" id="question"></p>
    <div class="actions">
      <button id="yes" class="danger"><span class="what" data-t="yes"></span></button>
      <button id="no" class="quiet"><span class="what" data-t="no"></span></button>
    </div>
  </div>

  <p id="working" hidden></p>
  <div class="actions"><button id="dismiss" class="quiet"><span class="what" data-t="close"></span></button></div>
</div>
<script>
  // The three languages this menu speaks.
  //
  // They are here rather than fetched because this page has to work on a
  // device with no network -- which is most of the reason it exists. Somebody
  // in a room in Osaka should not have to read English to put their screen
  // back on the wireless.
  //
  // Switching is immediate and remembered in this browser, so the next person
  // to open the menu on this screen gets the language the last one chose.
  const SAID = {
    en: {
      "wireless-is": "Wireless:", "not-connected": "not connected", "up-for": "up",
      "next": "Show the next item", "next-why": "Move the screen on now",
      "reload": "Reload what is on screen",
      "reload-why": "For a dashboard that has stopped updating",
      "network": "Set up the network",
      "network-why": "Join a wireless network, or give this screen a fixed address",
      "restart-browser": "Restart the browser",
      "restart-browser-why": "The screen goes black for a few seconds",
      "restart-display": "Restart the screen",
      "restart-display-why": "Rebuilds the display itself; takes longer",
      "wireless-again": "Set up wireless again",
      "wireless-again-why": "Forgets this network and shows the setup code",
      "tab-wireless": "Wireless", "tab-wired": "Wired",
      "join": "Join", "look-again": "Look again", "back": "Back",
      "apply": "Apply", "close": "Close", "yes": "Yes, do it", "no": "No, go back",
      "password-for": "Password for", "locked": "locked",
      "looking": "Looking for networks…", "nothing-in-range": "Nothing in range.",
      "scan-failed": "The search did not work.",
      "which-interface": "Which connection", "how-address": "How it gets an address",
      "by-dhcp": "Ask the network (DHCP)", "by-address": "Use the address below",
      "address-label": "Address and prefix", "gateway-label": "Gateway",
      "dns-label": "Name servers, separated by spaces",
      "ask-restart-browser": "Restart the browser? The screen goes black for a few seconds.",
      "ask-restart-display": "Restart the screen? It rebuilds the display and takes longer.",
      "ask-wireless": "Forget this wireless network and show the setup code?",
      "doing-next": "Moving on.", "doing-reload": "Reloading.",
      "doing-restart-browser": "Restarting the browser.",
      "doing-restart-display": "Restarting the screen.",
      "doing-wireless": "Setting up. The code will be on this screen in a moment.",
      "doing-join": "Joining {0}. This screen may lose its connection for a moment.",
      "doing-wired": "Setting up {0}. This screen may lose its connection for a moment.",
    },
    zh: {
      "wireless-is": "无线：", "not-connected": "未连接", "up-for": "已运行",
      "next": "显示下一项", "next-why": "立即切换到下一个内容",
      "reload": "重新加载当前页面",
      "reload-why": "适用于已停止更新的看板",
      "network": "设置网络",
      "network-why": "连接无线网络，或为此屏幕设置固定地址",
      "restart-browser": "重启浏览器",
      "restart-browser-why": "屏幕会黑屏几秒钟",
      "restart-display": "重启显示",
      "restart-display-why": "重建显示系统，耗时较长",
      "wireless-again": "重新设置无线网络",
      "wireless-again-why": "忘记当前网络并显示设置二维码",
      "tab-wireless": "无线", "tab-wired": "有线",
      "join": "连接", "look-again": "重新搜索", "back": "返回",
      "apply": "应用", "close": "关闭", "yes": "确定", "no": "取消",
      "password-for": "密码：", "locked": "加密",
      "looking": "正在搜索网络…", "nothing-in-range": "附近没有可用网络。",
      "scan-failed": "搜索失败。",
      "which-interface": "选择网络接口", "how-address": "地址获取方式",
      "by-dhcp": "自动获取（DHCP）", "by-address": "使用下面填写的地址",
      "address-label": "地址和前缀长度", "gateway-label": "网关",
      "dns-label": "DNS 服务器，用空格分隔",
      "ask-restart-browser": "重启浏览器？屏幕会黑屏几秒钟。",
      "ask-restart-display": "重启显示？将重建显示系统，耗时较长。",
      "ask-wireless": "忘记当前无线网络并显示设置二维码？",
      "doing-next": "正在切换。", "doing-reload": "正在重新加载。",
      "doing-restart-browser": "正在重启浏览器。",
      "doing-restart-display": "正在重启显示。",
      "doing-wireless": "正在设置。稍后此屏幕上会显示设置二维码。",
      "doing-join": "正在连接 {0}。此屏幕可能会短暂断开连接。",
      "doing-wired": "正在设置 {0}。此屏幕可能会短暂断开连接。",
    },
    ja: {
      "wireless-is": "無線：", "not-connected": "未接続", "up-for": "稼働",
      "next": "次の項目を表示", "next-why": "今すぐ次の内容に切り替えます",
      "reload": "表示中のページを再読み込み",
      "reload-why": "更新が止まったダッシュボード向け",
      "network": "ネットワークを設定",
      "network-why": "無線ネットワークに接続、またはこの画面に固定アドレスを設定します",
      "restart-browser": "ブラウザを再起動",
      "restart-browser-why": "画面が数秒間暗くなります",
      "restart-display": "ディスプレイを再起動",
      "restart-display-why": "表示システムを作り直します。少し時間がかかります",
      "wireless-again": "無線を設定し直す",
      "wireless-again-why": "現在のネットワークを削除し、設定用コードを表示します",
      "tab-wireless": "無線", "tab-wired": "有線",
      "join": "接続", "look-again": "再検索", "back": "戻る",
      "apply": "適用", "close": "閉じる", "yes": "実行する", "no": "やめる",
      "password-for": "パスワード：", "locked": "保護",
      "looking": "ネットワークを検索しています…", "nothing-in-range": "圏内にネットワークがありません。",
      "scan-failed": "検索に失敗しました。",
      "which-interface": "接続を選択", "how-address": "アドレスの取得方法",
      "by-dhcp": "自動で取得する（DHCP）", "by-address": "下のアドレスを使う",
      "address-label": "アドレスとプレフィックス", "gateway-label": "ゲートウェイ",
      "dns-label": "DNSサーバー（スペース区切り）",
      "ask-restart-browser": "ブラウザを再起動しますか？画面が数秒間暗くなります。",
      "ask-restart-display": "ディスプレイを再起動しますか？表示システムを作り直すため、少し時間がかかります。",
      "ask-wireless": "現在の無線ネットワークを削除して、設定用コードを表示しますか？",
      "doing-next": "次に進みます。", "doing-reload": "再読み込みしています。",
      "doing-restart-browser": "ブラウザを再起動しています。",
      "doing-restart-display": "ディスプレイを再起動しています。",
      "doing-wireless": "設定中です。まもなくこの画面に設定用コードが表示されます。",
      "doing-join": "{0} に接続しています。この画面は一時的に接続が切れることがあります。",
      "doing-wired": "{0} を設定しています。この画面は一時的に接続が切れることがあります。",
    },
  };

  let language = "en";
  try {
    const remembered = localStorage.getItem("cue.language");
    if (remembered && SAID[remembered]) language = remembered;
  } catch (error) {
    // A browser with storage switched off. English, then.
  }

  function say(key, ...values) {
    const words = (SAID[language] || SAID.en)[key] || SAID.en[key] || key;
    return values.reduce((text, value, index) =>
      text.replace("{" + index + "}", value), words);
  }

  function speak(chosen) {
    if (chosen) {
      language = chosen;
      try { localStorage.setItem("cue.language", chosen); } catch (error) {}
    }
    document.documentElement.lang = language;
    document.querySelectorAll("[data-t]").forEach((node) => {
      node.textContent = say(node.dataset.t);
    });
    document.querySelectorAll(".languages button").forEach((button) => {
      button.className = button.dataset.language === language ? "on" : "";
    });
    // The parts drawn by script rather than markup.
    if (chosenNetwork) document.getElementById("chosen").textContent = chosenNetwork;
    // Captured once, and "captured" is its own flag: an empty network name is
    // a real answer -- it means nothing is joined -- so testing the name
    // itself for emptiness read the words "not connected" back as though they
    // were the name of a network, and the second language never took.
    const joined = document.getElementById("joined");
    if (!joined.dataset.captured) {
      joined.dataset.captured = "yes";
      joined.dataset.ssid = joined.textContent.trim();
    }
    joined.textContent = joined.dataset.ssid || say("not-connected");
    if (!scanned) showScanning();
  }

  document.querySelectorAll(".languages button").forEach((button) => {
    button.addEventListener("click", () => speak(button.dataset.language));
  });

  const actions = document.getElementById("actions");
  const confirm = document.getElementById("confirm");
  const question = document.getElementById("question");
  const working = document.getElementById("working");
  const network = document.getElementById("network");
  const panelWireless = document.getElementById("wireless");
  const panelWired = document.getElementById("wired");
  let chosenNetwork = null, chosenSecured = false, scanned = false;

  fetch("/api/v1/playlist/hold", { method: "POST" }).catch(() => {});

  function close() {
    fetch("/api/v1/playlist/release", { method: "POST" }).catch(() => {});
    parent.postMessage("cue:close-menu", "*");
  }

  function openNetwork() {
    actions.hidden = true;
    network.hidden = false;
    loadInterfaces();
    scan();
  }

  document.getElementById("back").addEventListener("click", () => {
    network.hidden = true;
    actions.hidden = false;
  });

  document.getElementById("tab-wireless").addEventListener("click", () => tab(true));
  document.getElementById("tab-wired").addEventListener("click", () => tab(false));

  function tab(wireless) {
    panelWireless.hidden = !wireless;
    panelWired.hidden = wireless;
    document.getElementById("tab-wireless").className = wireless ? "on" : "";
    document.getElementById("tab-wired").className = wireless ? "" : "on";
  }

  function showScanning() {
    const list = document.getElementById("networks");
    list.textContent = "";
    const note = document.createElement("p");
    note.className = "facts";
    note.style.margin = "1.2vmin";
    note.textContent = say("looking");
    list.appendChild(note);
  }

  function scan() {
    scanned = false;
    showScanning();
    const list = document.getElementById("networks");
    fetch("/api/v1/menu/network/scan", { method: "POST" })
      .then((answer) => answer.json())
      .then((found) => {
        scanned = true;
        list.textContent = "";
        const networks = found.networks || [];
        if (!networks.length) return note(list, say("nothing-in-range"));
        for (const one of networks) {
          list.appendChild(networkRow(one, list));
        }
      })
      .catch(() => { scanned = true; list.textContent = ""; note(list, say("scan-failed")); });
  }

  function note(list, words) {
    const line = document.createElement("p");
    line.className = "facts";
    line.style.margin = "1.2vmin";
    line.textContent = words;
    list.appendChild(line);
  }

  function networkRow(one, list) {
    const button = document.createElement("button");
    button.disabled = !one.joinable;

    const name = document.createElement("span");
    name.className = "ssid";
    name.textContent = one.ssid;
    button.appendChild(name);

    if (one.secured) {
      const lock = document.createElement("span");
      lock.className = "lock";
      lock.dataset.t = "locked";
      lock.textContent = say("locked");
      button.appendChild(lock);
    }

    const bars = document.createElement("span");
    bars.className = "bars";
    for (let level = 1; level <= 4; level++) {
      const bar = document.createElement("i");
      if (level <= one.bars) bar.className = "on";
      bar.style.height = (level * 0.55 + 0.6) + "vmin";
      bars.appendChild(bar);
    }
    button.appendChild(bars);

    button.addEventListener("click", () => {
      list.querySelectorAll("button").forEach((other) => { other.className = ""; });
      button.className = "on";
      chosenNetwork = one.ssid;
      chosenSecured = one.secured;
      document.getElementById("chosen").textContent = one.ssid;
      document.getElementById("secret").hidden = !one.secured;
      if (one.secured) document.getElementById("passphrase").focus();
    });
    return button;
  }

  document.getElementById("rescan").addEventListener("click", scan);

  document.getElementById("join").addEventListener("click", () => {
    if (!chosenNetwork) return;
    working_(say("doing-join", chosenNetwork));
    fetch("/api/v1/menu/network/wireless", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        ssid: chosenNetwork,
        passphrase: chosenSecured ? document.getElementById("passphrase").value : "",
      }),
    }).catch(() => {}).finally(() => setTimeout(close, 2500));
  });

  function loadInterfaces() {
    fetch("/api/v1/menu/network")
      .then((answer) => answer.json())
      .then((state) => {
        const chooser = document.getElementById("wired-interface");
        chooser.textContent = "";
        for (const one of (state.interfaces || [])) {
          if (one.kind === "wireless") continue;
          const option = document.createElement("option");
          option.value = one.name;
          option.textContent = one.name + (one.addresses && one.addresses.length
            ? "  ·  " + one.addresses.join(", ") : "");
          chooser.appendChild(option);
        }
      })
      .catch(() => {});
  }

  document.getElementById("wired-method").addEventListener("change", (event) => {
    document.getElementById("fixed").hidden = event.target.value !== "static";
  });

  document.getElementById("apply").addEventListener("click", () => {
    const name = document.getElementById("wired-interface").value;
    if (!name) return;
    const dns = document.getElementById("wired-dns").value.trim();
    working_(say("doing-wired", name));
    fetch("/api/v1/menu/network/wired", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        interface: name,
        method: document.getElementById("wired-method").value,
        address: document.getElementById("wired-address").value.trim(),
        gateway: document.getElementById("wired-gateway").value.trim(),
        nameservers: dns ? dns.split(/\s+/) : [],
      }),
    }).catch(() => {}).finally(() => setTimeout(close, 2500));
  });

  function working_(words) {
    network.hidden = true;
    actions.hidden = true;
    confirm.hidden = true;
    working.hidden = false;
    working.textContent = words;
  }

  const doing = {
    "next": { call: "/api/v1/playlist/next", ask: null, said: "doing-next" },
    "reload": { call: "/api/v1/menu/reload", ask: null, said: "doing-reload" },
    "restart-browser": { call: "/api/v1/menu/restart/browser",
      ask: "ask-restart-browser", said: "doing-restart-browser" },
    "restart-display": { call: "/api/v1/menu/restart/display",
      ask: "ask-restart-display", said: "doing-restart-display" },
    "wireless": { call: "/api/v1/wireless/reset",
      ask: "ask-wireless", said: "doing-wireless" },
  };

  actions.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-do]");
    if (!button) return;
    if (button.dataset.do === "network") return openNetwork();

    const what = doing[button.dataset.do];
    if (!what) return;
    if (!what.ask) return run(what);

    question.textContent = say(what.ask);
    actions.hidden = true;
    confirm.hidden = false;
    document.getElementById("yes").onclick = () => run(what);
    document.getElementById("no").onclick = () => {
      confirm.hidden = true;
      actions.hidden = false;
    };
  });

  function run(what) {
    working_(say(what.said));
    fetch(what.call, { method: "POST" })
      .catch(() => {})
      .finally(() => setTimeout(close, 1200));
  }

  document.getElementById("dismiss").addEventListener("click", close);
  window.addEventListener("keydown", (event) => { if (event.key === "Escape") close(); });

  speak();
</script>
</body>
</html>
`))

// menuReload reloads whatever is on the screen, for a dashboard that has
// quietly stopped updating.
func (self *Server) menuReload(response http.ResponseWriter, request *http.Request) {
	browser := self.device.Browser()
	if browser == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no browser")
		return
	}
	if err := browser.ReloadCurrent(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"reloading": true})
}

// menuRestart restarts one of the programs the screen is made of.
//
// Only the two worth offering to somebody standing at a screen: the browser,
// for a page that has wedged, and the display, for everything else. Anything
// larger is a power cycle, which they can also do.
func (self *Server) menuRestart(response http.ResponseWriter, request *http.Request) {
	program := mux.Vars(request)["program"]
	switch program {
	case "browser", "display":
	default:
		writeError(response, http.StatusBadRequest,
			fmt.Sprintf("%q is not something the menu restarts", program))
		return
	}

	// Answered before doing it: restarting takes the screen down, and this
	// request came from the page on it.
	writeJSON(response, http.StatusOK, map[string]interface{}{"restarting": program})

	go func() {
		defer deferutil.Recover()
		if err := self.device.Restart(context.Background(), program); err != nil {
			log.Warningf("cannot restart the %s: %s", program, err)
		}
	}()
}
