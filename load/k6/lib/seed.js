// Seed-id приходят из env: k6 не умеет в SQL, их получает load/Taskfile.yml (psql) и передаёт
// SEED_ADDRESS_ID и SEED_PRODUCT_IDS (через запятую).

const maxItems = 3;
const maxQuantity = 5;

function required(name) {
  const value = __ENV[name];
  if (value === undefined || value.trim() === '') {
    throw new Error(`env ${name} is empty: seed ids are not passed to k6`);
  }
  return value.trim();
}

// loadSeed разбирает env и падает при пустом значении, чтобы не слать заведомо неверные запросы.
export function loadSeed() {
  const productIds = required('SEED_PRODUCT_IDS')
    .split(',')
    .map((id) => id.trim())
    .filter((id) => id !== '');
  if (productIds.length === 0) {
    throw new Error('env SEED_PRODUCT_IDS has no product ids');
  }
  return { addressId: required('SEED_ADDRESS_ID'), productIds };
}

function randomInt(min, max) {
  return min + Math.floor(Math.random() * (max - min + 1));
}

// randomOrder собирает тело POST /api/v1/orders: 1–3 разных товара, количество 1–5.
export function randomOrder(seed) {
  const pool = seed.productIds.slice();
  const count = Math.min(randomInt(1, maxItems), pool.length);
  const items = [];
  for (let i = 0; i < count; i++) {
    const idx = randomInt(0, pool.length - 1);
    items.push({ product_id: pool.splice(idx, 1)[0], quantity: randomInt(1, maxQuantity) });
  }
  return { address_id: seed.addressId, items };
}
