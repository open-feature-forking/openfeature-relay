// Pins what load.js's watch() relies on: client.close() on an open stream fires error ("canceled by client") and then
// end, so a planned close can be told from a stream that dropped. Run by TestStreamCloseEvents; a k6 upgrade that
// changes the events fails there.
import { Client, Stream } from 'k6/net/grpc';

const client = new Client();
client.loadProtoset(__ENV.PROTOSET);

export const options = { vus: 1, iterations: 1 };

export default async function () {
  const events = [];
  client.connect(`${__ENV.HOST}:${__ENV.SYNC_PORT}`, { plaintext: true });
  const stream = new Stream(client, '/flagd.sync.v1.FlagSyncService/SyncFlags', null);
  let payloads = 0;
  stream.on('data', () => payloads++);
  stream.on('error', (e) => events.push('error:' + (e && e.message ? e.message : e)));
  stream.on('end', () => events.push('end'));
  stream.write({});
  await new Promise((resolve) => setTimeout(resolve, 1500));
  events.push('close');
  client.close();
  await new Promise((resolve) => setTimeout(resolve, 500));
  console.log('CLOSE ' + JSON.stringify({ payloads, events }));
}
