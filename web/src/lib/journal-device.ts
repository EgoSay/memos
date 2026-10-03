/** This identifies local drafts only; it never authenticates a server request. */
const DEVICE_OWNER_KEY = "memos-journal-device-owner";
export type DeviceOwner = { name: string; username: string };
export function rememberJournalOwner(owner: DeviceOwner) {
  try {
    localStorage.setItem(DEVICE_OWNER_KEY, JSON.stringify({ name: owner.name, username: owner.username }));
  } catch {
    /* Device storage may be unavailable. */
  }
}
export function forgetJournalOwner() {
  try {
    localStorage.removeItem(DEVICE_OWNER_KEY);
  } catch {
    /* No local access remains available. */
  }
}
export function readJournalOwner(): DeviceOwner | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(DEVICE_OWNER_KEY) ?? "null");
    if (value && typeof value.name === "string" && /^users\/[^/]+$/.test(value.name) && typeof value.username === "string") return value;
  } catch {
    /* A corrupt marker grants no local access. */
  }
  return undefined;
}
