import { systemClient } from "../api/clients";

export function getBootstrap() {
  return systemClient.getBootstrap({});
}
