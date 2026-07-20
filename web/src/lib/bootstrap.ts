import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { SystemService } from "../gen/sumi/system/v1/system_pb";

const client = createClient(
  SystemService,
  createConnectTransport({ baseUrl: window.location.origin }),
);

export function getBootstrap() {
  return client.getBootstrap({});
}
