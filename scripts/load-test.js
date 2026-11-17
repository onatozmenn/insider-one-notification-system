import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: 20,
  duration: '30s',
  thresholds: {
    checks: ['rate>0.99'],
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const RUN_ID = Date.now();

export default function () {
  const suffix = `${__VU}-${__ITER}`;
  const payload = JSON.stringify({
    recipient: '+15555550100',
    channel: 'sms',
    content: `Load test ${suffix}`,
    priority: 'normal',
  });

  const response = http.post(`${BASE_URL}/api/v1/notifications`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'Idempotency-Key': `k6-${RUN_ID}-${suffix}`,
    },
  });

  check(response, {
    'accepted with 202': (res) => res.status === 202,
  });
}
