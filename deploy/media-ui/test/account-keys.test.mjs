import test from 'node:test'
import assert from 'node:assert/strict'
import '../account-keys.js'
const {createClient, usable} = globalThis.MeteorAccountKeys
const key = {id: 1, user_id: 7, group_id: 23, status: 'active', name: '绘图', key: 'test-secret', quota: 0}

test('only own active unexpired keys in the matching media group are usable', () => {
  assert.ok(usable(key, 7, 23))
  for (const patch of [{user_id: 8}, {group_id: 24}, {status:'disabled'}, {expires_at:'2000-01-01'}, {quota:1,quota_used:1}, {key:''}])
    assert.equal(usable({...key,...patch},7,23),false)
})
test('pagination, secret-free choices, and logout/account-change invalidation', async () => {
  let token = 'session-a'
  const paths = []
  const client = createClient({readToken:()=>token, fetchJSON:async (path,t)=>{
    assert.equal(t,token); paths.push(path)
    if (path.endsWith('/me')) return {id:7}
    if (path.endsWith('/groups/available')) return [
      {id:23,allow_image_generation:true,image_price_1k:0.134,image_price_2k:0.201,image_price_4k:0.268},
      {id:24,allow_image_generation:false,image_price_1k:null,image_price_2k:null,image_price_4k:null},
    ]
    return path.includes('page=1&') ? {items:[key],total:2} : {items:[{...key,id:2,group_id:24}],total:2}
  }})
  await client.refresh()
  assert.equal(paths.length,4)
  assert.deepEqual(client.list('image'),[{id:'1',name:'绘图'}])
  assert.equal(client.get('image','1'),'test-secret')
  assert.deepEqual(client.getInfo('image','1'),{id:'1',name:'绘图',groupId:23})
  assert.deepEqual(client.getGroup('image','1'),{
    id:23,
    allowImageGeneration:true,
    imagePrices:{'1K':0.134,'2K':0.201,'4K':0.268},
  })
  assert.equal(client.get('video','1'),'')
  token='session-b'
  assert.equal(client.get('image','1'),'')
  assert.deepEqual(client.list('image'),[])
})
test('no token means no key API requests; changed session during fetch is rejected', async () => {
  const anonymous=createClient({readToken:()=>'',fetchJSON:()=>assert.fail('must not fetch')})
  await assert.rejects(anonymous.refresh(),/登录/)
  let token='a'
  const raced=createClient({readToken:()=>token,fetchJSON:async path=>{
    if(path.endsWith('/me')) return {id:7}
    if(path.endsWith('/groups/available')) return []
    token='b';return {items:[key],total:1}
  }})
  await assert.rejects(raced.refresh(),/变化/)
  assert.equal(raced.get('image','1'),'')
})

test('default media-group discovery follows visible capabilities instead of fixed IDs', async () => {
  const dynamic = createClient({readToken:()=> 'session', fetchJSON:async path => {
    if (path.endsWith('/me')) return {id:7}
    if (path.endsWith('/groups/available')) return [
      {id:91, status:'active', allow_image_generation:true, image_price_1k:0.1},
      {id:107, status:'active', video_price_720p:0.02},
      {id:23, status:'active', allow_image_generation:false},
    ]
    return {items: [
      {...key, id:3, group_id:91, user_id:7, key:'image-secret'},
      {...key, id:4, group_id:107, user_id:7, key:'video-secret'},
      {...key, id:5, group_id:23, user_id:7, key:'other-secret'},
    ], total:3}
  }})
  await dynamic.refresh()
  assert.deepEqual(dynamic.list('image'), [{id:'3', name:'绘图'}])
  assert.deepEqual(dynamic.list('video'), [{id:'4', name:'绘图'}])
  assert.equal(dynamic.get('image', '3'), 'image-secret')
  assert.equal(dynamic.get('image', '4'), '')
})
