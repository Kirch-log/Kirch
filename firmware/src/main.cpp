// Kirch Wi-Fi probe.
//
// 1. First boot: no Wi-Fi saved -> advertises over Bluetooth as "KIRCH_xxxx".
//    Use Espressif's "ESP BLE Provisioning" phone app to send Wi-Fi name +
//    password (proof-of-possession code: PROV_POP in probe_config.h).
// 2. Joins Wi-Fi, announces itself as kirch-probe.local (mDNS, service _kirch._tcp).
// 3. WebSocket server on port 81. Every WINDOW_MS it broadcasts one "capture"
//    frame. Frames are numbered; clients can ask for missed ones again.
//
// Messages (JSON text):
//   probe -> client  {"type":"hello", ...}     on connect
//                    {"type":"capture","seq":N, ...} every window
//                    {"type":"gap","from":A,"oldest":B} if asked for frames no longer held
//   client -> probe  {"type":"resend","from":N}

#include <Arduino.h>
#include <ArduinoJson.h>
#include <ESPmDNS.h>
#include <WebSocketsServer.h>
#include <WiFi.h>
#include <WiFiProv.h>
#include <esp_random.h>
#include <time.h>

#include <string>

#include "capture.h"
#include "frame_ring.h"
#include "probe_config.h"

#ifndef KIRCH_FIRMWARE_VERSION
#define KIRCH_FIRMWARE_VERSION "dev"
#endif

namespace {

WebSocketsServer ws(WS_PORT);
FrameRing<RESEND_BUFFER_FRAMES> sentFrames;

char deviceId[24];   // kirch-probe-xxxxxx (last 3 MAC bytes)
char bleName[16];    // KIRCH_xxxxxx
uint32_t bootId = 0; // random per boot: lets clients see that seq restarted
uint32_t nextSeq = 1;
volatile bool wifiUp = false;
bool networkStarted = false;

void makeIds() {
  uint8_t mac[6];
  WiFi.macAddress(mac);
  snprintf(deviceId, sizeof(deviceId), "kirch-probe-%02x%02x%02x", mac[3], mac[4], mac[5]);
  snprintf(bleName, sizeof(bleName), "KIRCH_%02X%02X%02X", mac[3], mac[4], mac[5]);
  bootId = esp_random();
}

bool resetButtonHeld() {
  pinMode(RESET_BUTTON_PIN, INPUT_PULLUP);
  const uint32_t start = millis();
  while (digitalRead(RESET_BUTTON_PIN) == LOW) {
    if (millis() - start >= RESET_HOLD_MS) return true;
    delay(10);
  }
  return false;
}

// Unix time in ms once SNTP has synced, else 0 (the laptop stamps time then).
uint64_t unixMs() {
  struct timeval tv;
  gettimeofday(&tv, nullptr);
  if (tv.tv_sec < 1700000000) return 0;
  return static_cast<uint64_t>(tv.tv_sec) * 1000 + tv.tv_usec / 1000;
}

std::string helloFrame() {
  JsonDocument doc;
  doc["type"] = "hello";
  JsonObject device = doc["device"].to<JsonObject>();
  device["id"] = deviceId;
  device["firmware"] = KIRCH_FIRMWARE_VERSION;
  doc["boot_id"] = bootId;
  doc["window_ms"] = WINDOW_MS;
  doc["newest_seq"] = sentFrames.newest();
  doc["oldest_seq"] = sentFrames.oldest();
  JsonObject probes = doc["probes"].to<JsonObject>();
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    JsonObject p = probes[PROBES[i].id].to<JsonObject>();
    p["pin"] = PROBES[i].pin;
    p["mode"] = modeName(PROBES[i].mode);
  }
  std::string out;
  serializeJson(doc, out);
  return out;
}

void sendText(uint8_t client, const std::string &text) {
  ws.sendTXT(client, reinterpret_cast<const uint8_t *>(text.data()), text.size());
}

void handleResend(uint8_t client, uint32_t from) {
  if (sentFrames.newest() == 0) return;
  if (from < sentFrames.oldest()) {
    JsonDocument gap;
    gap["type"] = "gap";
    gap["from"] = from;
    gap["oldest"] = sentFrames.oldest();
    std::string out;
    serializeJson(gap, out);
    sendText(client, out);
  }
  sentFrames.forEachFrom(from, [client](uint32_t, const std::string &frame) { sendText(client, frame); });
}

void onWsEvent(uint8_t client, WStype_t type, uint8_t *payload, size_t length) {
  switch (type) {
  case WStype_CONNECTED:
    Serial.printf("[ws] client %u connected\n", client);
    sendText(client, helloFrame());
    break;
  case WStype_DISCONNECTED:
    Serial.printf("[ws] client %u disconnected\n", client);
    break;
  case WStype_TEXT: {
    JsonDocument msg;
    if (deserializeJson(msg, payload, length)) return; // ignore bad JSON
    if (msg["type"] == "resend") handleResend(client, msg["from"] | 0u);
    break;
  }
  default:
    break;
  }
}

void onWifiEvent(arduino_event_t *event) {
  switch (event->event_id) {
  case ARDUINO_EVENT_PROV_START:
    Serial.printf("[prov] waiting for Wi-Fi details over Bluetooth as %s (code %s)\n", bleName, PROV_POP);
    break;
  case ARDUINO_EVENT_PROV_CRED_RECV:
    Serial.printf("[prov] got Wi-Fi \"%s\"\n", reinterpret_cast<const char *>(event->event_info.prov_cred_recv.ssid));
    break;
  case ARDUINO_EVENT_PROV_CRED_FAIL:
    Serial.println("[prov] could not join that Wi-Fi; try again from the app");
    break;
  case ARDUINO_EVENT_WIFI_STA_GOT_IP:
    Serial.printf("[wifi] connected, IP %s\n", WiFi.localIP().toString().c_str());
    wifiUp = true;
    break;
  case ARDUINO_EVENT_WIFI_STA_DISCONNECTED:
    if (wifiUp) Serial.println("[wifi] lost connection, reconnecting");
    wifiUp = false;
    break;
  default:
    break;
  }
}

// Started once, after the first IP. mDNS/WebSocket keep working across reconnects.
void startNetworkServices() {
  if (MDNS.begin(MDNS_HOSTNAME)) {
    MDNS.addService("kirch", "tcp", WS_PORT);
    MDNS.addServiceTxt("kirch", "tcp", "id", deviceId);
    MDNS.addServiceTxt("kirch", "tcp", "fw", KIRCH_FIRMWARE_VERSION);
    Serial.printf("[mdns] ws://%s.local:%u/\n", MDNS_HOSTNAME, WS_PORT);
  } else {
    Serial.println("[mdns] failed to start; connect by IP instead");
  }
  configTime(0, 0, "pool.ntp.org", "time.google.com");
  ws.begin();
  ws.onEvent(onWsEvent);
  networkStarted = true;
}

void publishCapture(const std::string &probesJson) {
  const uint32_t seq = nextSeq++;
  // Build the frame around the already-serialized probes object.
  std::string frame;
  frame.reserve(probesJson.size() + 160);
  frame += "{\"type\":\"capture\",\"seq\":";
  frame += std::to_string(seq);
  frame += ",\"boot_id\":";
  frame += std::to_string(bootId);
  frame += ",\"device_id\":\"";
  frame += deviceId;
  frame += "\",\"uptime_ms\":";
  frame += std::to_string(millis());
  const uint64_t now = unixMs();
  if (now) {
    frame += ",\"unix_ms\":";
    frame += std::to_string(now);
  }
  frame += ",\"window_ms\":";
  frame += std::to_string(WINDOW_MS);
  frame += ",\"probes\":";
  frame += probesJson;
  frame += "}";

  sentFrames.push(seq, frame);
  if (networkStarted) ws.broadcastTXT(reinterpret_cast<const uint8_t *>(frame.data()), frame.size());
}

} // namespace

void setup() {
  Serial.begin(115200);
  delay(200);
  makeIds();
  Serial.printf("\nKirch probe %s, firmware %s\n", deviceId, KIRCH_FIRMWARE_VERSION);

  const bool forgetWifi = resetButtonHeld();
  if (forgetWifi) Serial.println("[prov] BOOT held: forgetting saved Wi-Fi");

  WiFi.onEvent(onWifiEvent);
  // Already provisioned -> joins saved Wi-Fi. Otherwise starts BLE provisioning,
  // then frees the Bluetooth stack once Wi-Fi details arrive.
  WiFiProv.beginProvision(WIFI_PROV_SCHEME_BLE, WIFI_PROV_SCHEME_HANDLER_FREE_BTDM,
                          WIFI_PROV_SECURITY_1, PROV_POP, bleName, nullptr, nullptr,
                          forgetWifi);

  capture::begin(); // measures even while offline; frames are buffered
}

void loop() {
  if (wifiUp && !networkStarted) startNetworkServices();
  if (networkStarted) ws.loop();

  std::string probesJson;
  if (capture::poll(millis(), probesJson)) publishCapture(probesJson);
  delay(1);
}
