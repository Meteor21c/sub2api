/* Same-origin user API only. Secrets remain in this closure, never in DOM/URLs. */
(() => {
  const usable = (key, userId, groupId, now = Date.now()) =>
    Number(key.user_id) === Number(userId) && Number(key.group_id) === Number(groupId) &&
    key.status === 'active' && typeof key.key === 'string' && key.key.length > 0 &&
    (!key.expires_at || Date.parse(key.expires_at) > now) &&
    (!(Number(key.quota) > 0) || Number(key.quota_used) < Number(key.quota));

  function createClient({ readToken, fetchJSON, groups = { image: 23, video: 24 } }) {
    let session = '', rows = [], availableGroups = [], userId = null;
    const clear = () => { session = ''; rows = []; availableGroups = []; userId = null; };
    const current = () => session && readToken() === session;
    async function refresh() {
      clear();
      const token = readToken();
      if (!token) throw new Error('请先登录，再使用图片或视频生成。');
      const [me, visibleGroups] = await Promise.all([
        fetchJSON('/api/v1/auth/me', token),
        fetchJSON('/api/v1/groups/available', token),
      ]);
      const found = [];
      for (let page = 1; page <= 100; page++) {
        const result = await fetchJSON(`/api/v1/keys?page=${page}&page_size=100&status=active`, token);
        const items = result.items || [];
        found.push(...items);
        if (items.length === 0 || found.length >= Number(result.total)) break;
        if (page === 100) throw new Error('密钥数量过多，请在密钥页面管理后重试。');
      }
      if (readToken() !== token) throw new Error('登录状态已变化，请重新读取密钥。');
      userId = me.id;
      rows = found;
      availableGroups = Array.isArray(visibleGroups) ? visibleGroups : [];
      session = token;
    }
    function list(kind) {
      if (!current()) { clear(); return []; }
      return rows.filter(k => usable(k, userId, groups[kind]))
        .map(k => ({ id: String(k.id), name: k.name || `密钥 ${k.id}` }));
    }
    function get(kind, id) {
      if (!current()) { clear(); return ''; }
      const row = rows.find(k => String(k.id) === String(id) && usable(k, userId, groups[kind]));
      return row?.key || '';
    }
    function getInfo(kind, id) {
      if (!current()) { clear(); return null; }
      const row = rows.find(k => String(k.id) === String(id) && usable(k, userId, groups[kind]));
      return row ? { id: String(row.id), name: row.name || `密钥 ${row.id}`, groupId: Number(row.group_id) } : null;
    }
    function getGroup(kind, id) {
      const info = getInfo(kind, id);
      if (!info) return null;
      const group = availableGroups.find(item => Number(item.id) === info.groupId);
      if (!group) return null;
      return {
        id: Number(group.id),
        allowImageGeneration: group.allow_image_generation === true,
        imagePrices: {
          '1K': group.image_price_1k,
          '2K': group.image_price_2k,
          '4K': group.image_price_4k,
        },
      };
    }
    function owns(kind, secret) {
      if (!current() || !secret) { clear(); return false; }
      return rows.some(k => k.key === secret && usable(k, userId, groups[kind]));
    }
    return { refresh, list, get, getInfo, getGroup, owns, clear };
  }
  globalThis.MeteorAccountKeys = { createClient, usable };
})();
