import { v4 as uuidv4 } from "uuid";

// crypto.randomUUID() throws on insecure-context origins (e.g. plain http on
// a non-localhost host); uuid's v4 falls back to crypto.getRandomValues.
export function generateAppSession(): string {
  return uuidv4();
}
