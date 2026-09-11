import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

// The opposite of hot_seat: every request picks its own seat out of a large
// pool, so collisions are rare and the numbers show what the service costs
// when nothing is contended.
const API = __ENV.API_URL || 'http://api:8080';
const EVENT_ID = Number(__ENV.EVENT_ID || 2);
const SEAT_FROM = Number(__ENV.SEAT_FROM || 1000001);
const SEATS = Number(__ENV.SEATS || 1000000);
const USERS = Number(__ENV.USERS || 500);

export const options = {
  scenarios: {
    spread: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 50),
      duration: __ENV.DURATION || '30s',
    },
  },
  thresholds: {
    booking_failed: ['count==0'],
    http_req_duration: ['p(95)<1000'],
  },
};

const won = new Counter('booking_won');
const conflicted = new Counter('booking_conflicted');
const failed = new Counter('booking_failed');

export default function () {
  const seatID = SEAT_FROM + Math.floor(Math.random() * SEATS);

  const res = http.post(
    `${API}/v1/bookings`,
    JSON.stringify({ event_id: EVENT_ID, seat_id: seatID }),
    {
      headers: {
        'Content-Type': 'application/json',
        'X-User-Id': String((__VU % USERS) + 1),
      },
      responseCallback: http.expectedStatuses(201, 409),
      tags: { name: 'POST /v1/bookings' },
    },
  );

  if (res.status === 201) won.add(1);
  else if (res.status === 409) conflicted.add(1);
  else failed.add(1);

  check(res, { 'settled as 201 or 409': (r) => r.status === 201 || r.status === 409 });
}
