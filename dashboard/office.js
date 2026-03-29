
// --- Agent World (SpriteEngine) ---
var spriteEngine = null;
var agentWorldOpen = localStorage.getItem('aw-open') === 'true';

var _spriteInitializing = false;

function toggleAgentWorld() {
  if (_spriteInitializing) return; // block clicks during init
  agentWorldOpen = !agentWorldOpen;
  localStorage.setItem('aw-open', agentWorldOpen);
  var container = document.getElementById('agent-world-container');
  if (agentWorldOpen) {
    container.style.display = '';
    if (!spriteEngine) {
      _spriteInitializing = true;
      spriteEngine = new SpriteEngine(document.getElementById('agent-world-cv'));
      spriteEngine.init().then(function() {
        _spriteInitializing = false;
      }).catch(function(e) {
        _spriteInitializing = false;
        spriteEngine = null; // allow retry on next toggle
        console.warn('SpriteEngine init failed', e);
      });
    } else {
      spriteEngine.start();
    }
  } else {
    container.style.display = 'none';
    if (spriteEngine) spriteEngine.stop();
  }
}

var _spriteConfigChecked = false;

function updateAgentWorldToggle(sprites) {
  var toggle = document.getElementById('agent-world-toggle');

  // One-time init: restore persisted open state on first call
  if (!_spriteConfigChecked) {
    _spriteConfigChecked = true;
    toggle.style.display = '';
    if (agentWorldOpen && !spriteEngine && !_spriteInitializing) {
      var ctr = document.getElementById('agent-world-container');
      ctr.style.display = '';
      _spriteInitializing = true;
      spriteEngine = new SpriteEngine(document.getElementById('agent-world-cv'));
      spriteEngine.init().then(function() {
        _spriteInitializing = false;
      }).catch(function(e) {
        _spriteInitializing = false;
        spriteEngine = null;
        console.warn('SpriteEngine restore init failed', e);
      });
    }
  }

  toggle.style.display = '';

  var working = 0, idle = 0;
  for (var k in sprites) {
    if (sprites[k] === 'idle') idle++;
    else working++;
  }
  var parts = [];
  if (working > 0) parts.push(working + ' working');
  if (idle > 0) parts.push(idle + ' idle');
  document.getElementById('aw-status').textContent = parts.length > 0 ? parts.join(' · ') : 'all idle';
  renderMinimap(sprites);
}

// --- Agent Colors (derived from GEM_TEAM for minimap, modals, etc.) ---
var AGENT_COLORS = {};
var DEFAULT_AGENT_COLOR = '#a78bfa';
(function() {
  for (var k in GEM_TEAM) { AGENT_COLORS[k] = GEM_TEAM[k].color; }
})();

// --- Mini-map ---
function renderMinimap(sprites) {
  var cv = document.getElementById('aw-minimap');
  if (!cv) return;
  var ctx = cv.getContext('2d');
  var W = 120, H = 40;
  var scaleX = W / OFFICE.W, scaleY = H / OFFICE.H;
  ctx.clearRect(0, 0, W, H);
  ctx.fillStyle = '#2a2218';
  ctx.fillRect(0, 0, W, H);
  // Draw room outlines
  var rooms = typeof OFFICE !== 'undefined' ? OFFICE.rooms : null;
  if (rooms) {
    ctx.strokeStyle = '#5a4a38';
    ctx.lineWidth = 0.5;
    for (var rk in rooms) {
      var r = rooms[rk];
      ctx.strokeRect(Math.round(r.x * scaleX), Math.round(r.y * scaleY),
        Math.round(r.w * scaleX), Math.round(r.h * scaleY));
    }
  }
  // Draw agent dots
  if (spriteEngine && spriteEngine.agents) {
    for (var name in spriteEngine.agents) {
      var a = spriteEngine.agents[name];
      var color = (typeof AGENT_COLORS !== 'undefined' && AGENT_COLORS[name]) || '#a78bfa';
      var ax = Math.round(a.x * scaleX);
      var ay = Math.round(a.y * scaleY);
      ctx.fillStyle = color;
      ctx.fillRect(ax - 1, ay - 1, 3, 3);
      // Pulse ring for working agents
      if (a.spriteState && a.spriteState !== 'idle') {
        var pulse = 0.4 + 0.6 * Math.abs(Math.sin(Date.now() / 400));
        ctx.globalAlpha = pulse;
        ctx.strokeStyle = color;
        ctx.lineWidth = 0.5;
        ctx.strokeRect(ax - 3, ay - 3, 7, 7);
        ctx.globalAlpha = 1;
      }
    }
  } else if (sprites) {
    // Fallback: no engine, place dots in room centers
    var idx = 0;
    for (var sn in sprites) {
      var c2 = (typeof AGENT_COLORS !== 'undefined' && AGENT_COLORS[sn]) || '#a78bfa';
      var fx = 30 + idx * 25, fy = 20;
      ctx.fillStyle = c2;
      ctx.fillRect(Math.round(fx * scaleX), Math.round(fy * scaleY), 3, 3);
      idx++;
    }
  }
}

// Mini-map click handler
(function() {
  var cv = document.getElementById('aw-minimap');
  if (!cv) return;
  cv.addEventListener('click', function(e) {
    e.stopPropagation();
    if (!spriteEngine || !spriteEngine.agents) return;
    var rect = cv.getBoundingClientRect();
    var scaleX = OFFICE.W / rect.width;
    var scaleY = OFFICE.H / rect.height;
    var mx = (e.clientX - rect.left) * scaleX;
    var my = (e.clientY - rect.top) * scaleY;
    // Find nearest agent
    var best = null, bestDist = Infinity;
    for (var name in spriteEngine.agents) {
      var a = spriteEngine.agents[name];
      var d = Math.sqrt((a.x - mx) * (a.x - mx) + (a.y - my) * (a.y - my));
      if (d < bestDist) { bestDist = d; best = a; }
    }
    if (best && bestDist < 80) {
      // Open office if closed
      if (!agentWorldOpen) toggleAgentWorld();
      // Flash the agent
      best._flashTimer = 1500;
    }
  });
})();

// Office layout constants (in canvas pixels, scaled for 600×200 logical size)
// Office uses Star-Office-UI background at 640x360 (half of 1280x720)
var OFFICE_PALETTE = {
  minimapBg: '#2a2218', minimapGrid: '#5a4a38'
};
var OFFICE = {
  W: 640, H: 360,
  WALL_H: 0,
  // Room zones mapped to Star-Office-UI background layout (at 640x360 scale)
  // Left room: library — used for meeting/review
  // Center: main hall — used for work (desk area)
  // Right-top: server room — used for error state
  // Right-bottom: break room (sofa) — used for idle/lounge
  rooms: {
    meeting: { x: 10, y: 40, w: 195, h: 280, label: '',
      furniture: [],
      seats: [{ x: 60, y: 200 }, { x: 140, y: 200 }, { x: 60, y: 280 }, { x: 140, y: 280 }]
    },
    work: { x: 215, y: 40, w: 210, h: 280, label: '',
      furniture: [],
      seats: [{ x: 260, y: 210 }, { x: 340, y: 210 }, { x: 300, y: 280 }]
    },
    lounge: { x: 435, y: 180, w: 195, h: 170, label: '',
      furniture: [],
      seats: [{ x: 480, y: 260 }, { x: 560, y: 260 }, { x: 480, y: 310 }, { x: 560, y: 310 }]
    }
  }
};

// State → target room mapping
var STATE_ROOM = {
  idle: 'lounge', work: 'work', think: 'work',
  talk: 'meeting', review: 'meeting',
  celebrate: null, error: null
};

function SpriteEngine(canvas) {
  this.canvas = canvas;
  this.ctx = canvas.getContext('2d');
  this.config = null;
  this.images = {};      // agentName -> Image (single-sheet) or null
  this.multiImages = {}; // agentName -> { stateName: Image } (multi-sheet)
  this.splitFrames = {}; // agentName -> { frames: [ImageBitmap...], cols, rows }
  this.agents = {};      // agentName -> agent object
  this.stateMap = {};    // stateName -> { row, frames }
  this.bgImage = null;
  this.running = false;
  this.rafId = null;
  this.lastTime = 0;
  this.frameTick = 0;
  this.seatClaims = {};  // "room:seatIdx" -> agentName
}

SpriteEngine.prototype.init = async function() {
  try {
    var res = await fetch(API + '/api/sprites/config');
    this.config = await res.json();
  } catch(e) {
    console.warn('SpriteEngine: failed to load config', e);
    this.config = { cellWidth: 32, cellHeight: 32, states: [], agents: {} };
  }
  // Validate cell dimensions
  if (!this.config.cellWidth || this.config.cellWidth <= 0) this.config.cellWidth = 32;
  if (!this.config.cellHeight || this.config.cellHeight <= 0) this.config.cellHeight = 32;

  // Build state lookup
  if (this.config.states) {
    for (var i = 0; i < this.config.states.length; i++) {
      var s = this.config.states[i];
      this.stateMap[s.name] = { row: s.row, frames: s.frames };
    }
  }

  // Load background image (custom or default office_bg)
  var bgImg = new Image();
  if (this.config.background) {
    bgImg.src = API + '/media/sprites/' + this.config.background;
  } else {
    bgImg.src = '/dashboard/office-bg.webp';
  }
  this.bgImage = bgImg;

  // Load sprite sheets — pre-split into individual frames via createImageBitmap.
  // This avoids ALL drawImage source-clipping issues by making each frame an independent bitmap.
  var agents = this.config.agents || {};
  var promises = [];
  var self = this;
  var cw = this.config.cellWidth || 32;
  var ch = this.config.cellHeight || 32;
  // Helper: load image as Image element (works in ALL browsers, no createImageBitmap needed)
  function _loadImg(url) {
    return new Promise(function(resolve, reject) {
      var img = new Image();
      img.onload = function() { resolve(img); };
      img.onerror = function() { reject('Failed to load: ' + url); };
      img.src = url;
    });
  }
  // Helper: split a loaded Image into individual frame canvases.
  // Each canvas is exactly cw×ch pixels — physically cannot show adjacent frames.
  function _splitSheet(img, cellW, cellH) {
    var cols = Math.max(1, Math.floor(img.width / cellW));
    var rows = Math.max(1, Math.floor(img.height / cellH));
    var frames = [];
    for (var r = 0; r < rows; r++) {
      for (var c = 0; c < cols; c++) {
        var fc = document.createElement('canvas');
        fc.width = cellW;
        fc.height = cellH;
        var fctx = fc.getContext('2d');
        fctx.drawImage(img, c * cellW, r * cellH, cellW, cellH, 0, 0, cellW, cellH);
        frames.push(fc);
      }
    }
    return { frames: frames, cols: cols, rows: rows };
  }

  for (var name in agents) {
    var def = agents[name];
    // Single-sheet: split into per-frame canvas elements
    if (def.sheet) {
      (function(n, sheet) {
        promises.push(
          _loadImg(API + '/media/sprites/' + sheet)
            .then(function(img) {
              var result = _splitSheet(img, cw, ch);
              self.splitFrames[n] = result;
              console.log('[Sprites] ' + n + ': ' + result.cols + 'x' + result.rows + ' = ' + result.frames.length + ' frames (canvas-split)');
            })
            .catch(function(e) { console.warn('[Sprites] Failed to split ' + n + ':', e); })
        );
      })(name, def.sheet);
    }
    // Multi-sheet mode: one image per state
    if (def.sheets && Object.keys(def.sheets).length > 0) {
      (function(n, sheets) {
        if (!self.splitFrames[n]) self.splitFrames[n] = { frames: [], cols: 0, rows: 0, perState: {} };
        for (var state in sheets) {
          (function(st, file) {
            promises.push(
              _loadImg(API + '/media/sprites/' + file)
                .then(function(img) {
                  var cols = Math.max(1, Math.floor(img.width / cw));
                  var frames = [];
                  for (var c = 0; c < cols; c++) {
                    var fc = document.createElement('canvas');
                    fc.width = cw; fc.height = ch;
                    var fctx = fc.getContext('2d');
                    fctx.drawImage(img, c * cw, 0, cw, ch, 0, 0, cw, ch);
                    frames.push(fc);
                  }
                  self.splitFrames[n].perState = self.splitFrames[n].perState || {};
                  self.splitFrames[n].perState[st] = frames;
                })
                .catch(function() {})
            );
          })(state, sheets[state]);
        }
      })(name, def.sheets);
    }
  }

  await Promise.all(promises);

  // Initialize agent objects
  for (var name in agents) {
    var startSeat = this._findFreeSeat('lounge', name);
    this.agents[name] = {
      name: name,
      x: startSeat ? startSeat.x : 300 + Math.random() * 100,
      y: startSeat ? startSeat.y : 100 + Math.random() * 40,
      targetX: 0, targetY: 0,
      moving: false,
      spriteState: 'idle',
      animState: 'idle',
      facing: 'right',
      frame: 0,
      idleTimer: 3000 + Math.random() * 5000,
      celebrateTimer: 0
    };
    var a = this.agents[name];
    a.targetX = a.x;
    a.targetY = a.y;
  }

  // Set canvas resolution
  this.canvas.width = OFFICE.W;
  this.canvas.height = OFFICE.H;

  if (Object.keys(this.agents).length > 0) {
    this.start();
  }
};

SpriteEngine.prototype.start = function() {
  if (this.running) return;
  this.running = true;
  this.lastTime = performance.now();
  var self = this;
  function loop(ts) {
    if (!self.running) return;
    var dt = ts - self.lastTime;
    self.lastTime = ts;
    // Cap dt to prevent runaway after tab switch (rAF pauses but timestamp jumps)
    if (dt > 100) dt = 16;
    self.frameTick += dt;
    self.update(dt);
    self.render();
    self.rafId = requestAnimationFrame(loop);
  }
  this.rafId = requestAnimationFrame(loop);
};

SpriteEngine.prototype.stop = function() {
  this.running = false;
  if (this.rafId) {
    cancelAnimationFrame(this.rafId);
    this.rafId = null;
  }
};

SpriteEngine.prototype._findFreeSeat = function(roomName, agentName) {
  var room = OFFICE.rooms[roomName];
  if (!room) return null;
  for (var i = 0; i < room.seats.length; i++) {
    var key = roomName + ':' + i;
    if (!this.seatClaims[key] || this.seatClaims[key] === agentName) {
      this.seatClaims[key] = agentName;
      return { x: room.seats[i].x, y: room.seats[i].y };
    }
  }
  // All seats taken — stand near room center
  return { x: room.x + room.w / 2 + (Math.random() - 0.5) * 30, y: room.y + room.h - 30 };
};

SpriteEngine.prototype._releaseSeat = function(agentName) {
  for (var key in this.seatClaims) {
    if (this.seatClaims[key] === agentName) {
      delete this.seatClaims[key];
    }
  }
};

SpriteEngine.prototype.updateAgentStates = function(sprites) {
  if (!sprites) return;
  for (var name in sprites) {
    var agent = this.agents[name];
    if (!agent) continue; // only track agents from config
    var newState = sprites[name];
    if (newState === agent.spriteState) continue;

    agent.spriteState = newState;

    // Special: celebrate/error stay in place
    if (newState === 'celebrate') {
      agent.animState = 'celebrate';
      agent.celebrateTimer = 3000;
      continue;
    }
    if (newState === 'error') {
      agent.animState = 'error';
      continue;
    }

    // Find target room
    var targetRoom = STATE_ROOM[newState] || 'lounge';
    this._releaseSeat(name);
    var seat = this._findFreeSeat(targetRoom, name);
    if (seat) {
      agent.targetX = seat.x;
      agent.targetY = seat.y;
      agent.moving = true;
    }
  }
};

SpriteEngine.prototype.update = function(dt) {
  var SPEED = 0.06; // pixels per ms
  // cellWidth reserved for future use

  for (var name in this.agents) {
    var a = this.agents[name];

    // Flash timer (minimap click highlight)
    if (a._flashTimer > 0) a._flashTimer -= dt;

    // Celebrate timer
    if (a.celebrateTimer > 0) {
      a.celebrateTimer -= dt;
      if (a.celebrateTimer <= 0) {
        a.spriteState = 'idle';
        a.animState = 'idle';
        var seat = this._findFreeSeat('lounge', name);
        if (seat) { a.targetX = seat.x; a.targetY = seat.y; a.moving = true; }
      }
      continue;
    }

    // Movement
    if (a.moving) {
      var dx = a.targetX - a.x;
      var dy = a.targetY - a.y;
      var dist = Math.sqrt(dx * dx + dy * dy);
      if (dist < 2) {
        a.x = a.targetX;
        a.y = a.targetY;
        a.moving = false;
        a.animState = a.spriteState;
        a.frame = 0;
      } else {
        var step = SPEED * dt;
        if (step > dist) step = dist;
        a.x += (dx / dist) * step;
        a.y += (dy / dist) * step;
        // Walk direction
        if (Math.abs(dx) > Math.abs(dy)) {
          a.animState = dx > 0 ? 'walk_right' : 'walk_left';
          a.facing = dx > 0 ? 'right' : 'left';
        } else {
          a.animState = dy > 0 ? 'walk_down' : 'walk_up';
        }
      }
    } else if (a.spriteState === 'idle') {
      // Idle wandering
      a.idleTimer -= dt;
      if (a.idleTimer <= 0) {
        a.idleTimer = 3000 + Math.random() * 5000;
        var room = OFFICE.rooms.lounge;
        a.targetX = room.x + 20 + Math.random() * (room.w - 40);
        a.targetY = room.y + 30 + Math.random() * (room.h - 50);
        a.moving = true;
      }
    }
  }

  // Advance animation frames (~200ms per frame)
  // Use modulo to prevent runaway: if frameTick somehow accumulates (e.g. init delay),
  // only advance one frame and reset cleanly.
  if (this.frameTick >= 200) {
    this.frameTick = this.frameTick % 200;
    for (var name in this.agents) {
      var a = this.agents[name];
      var stDef = this.stateMap[a.animState];
      if (stDef) {
        a.frame = (a.frame + 1) % stDef.frames;
      }
    }
  }
};

SpriteEngine.prototype.render = function() {
  var ctx = this.ctx;
  var W = OFFICE.W, H = OFFICE.H;
  ctx.clearRect(0, 0, W, H);

  if (this.bgImage && this.bgImage.complete && this.bgImage.naturalWidth > 0) {
    ctx.drawImage(this.bgImage, 0, 0, W, H);
  } else {
    this._drawOffice(ctx);
  }

  // Draw decorations (between office and agents)
  this._drawDecorations(ctx);

  // Draw agents (sorted by y for depth)
  var sorted = [];
  for (var name in this.agents) sorted.push(this.agents[name]);
  sorted.sort(function(a, b) { return a.y - b.y; });

  for (var i = 0; i < sorted.length; i++) {
    this._drawAgent(ctx, sorted[i]);
  }

  // Draw edit overlay on top
  this._drawDecorEditOverlay(ctx);

  // Update minimap when office is rendering
  renderMinimap(null);
};

SpriteEngine.prototype._drawOffice = function(ctx) {
  // Fallback when background image hasn't loaded yet
  ctx.fillStyle = '#2a2218';
  ctx.fillRect(0, 0, OFFICE.W, OFFICE.H);
};

// Built-in sprite frames — loaded as PNG, split using createImageBitmap (browser-native crop).
// Each agent gets an array of 8 ImageBitmap frames: [row0col0, row0col1, ..., row1col0, ...]
var BUILTIN_SPRITE_FW = 32, BUILTIN_SPRITE_FH = 32, BUILTIN_SPRITE_COLS = 4, BUILTIN_SPRITE_ROWS = 2;
var SPRITE_FRAMES = {}; // { agentName: [ImageBitmap, ...] }

// Gem team identity — color palette, glow, and canvas filter for each agent
var GEM_TEAM = {
  ruri:       { color: '#5599ff', glow: 'rgba(85,153,255,0.28)',   label: '琉璃', filter: '' },
  hisui:      { color: '#44cc88', glow: 'rgba(68,204,136,0.28)',   label: '翡翠', filter: '' },
  kokuyou:    { color: '#9977bb', glow: 'rgba(102,68,140,0.28)',   label: '黒曜', filter: 'hue-rotate(240deg) brightness(0.65) saturate(1.3)' },
  kohaku:     { color: '#eebb33', glow: 'rgba(238,187,51,0.28)',   label: '琥珀', filter: '' },
  kougyoku:   { color: '#cc2244', glow: 'rgba(204,34,68,0.28)',    label: '紅玉', filter: 'hue-rotate(330deg) saturate(2.2) brightness(0.85)' },
  daiya:      { color: '#ddeeff', glow: 'rgba(221,238,255,0.28)',  label: 'ダイヤ', filter: 'saturate(0.15) brightness(1.6)' },
  spinel:     { color: '#dd2299', glow: 'rgba(221,34,153,0.28)',   label: '尖晶', filter: 'hue-rotate(300deg) saturate(2.0) brightness(0.9)' },
  kirara:     { color: '#ccaa66', glow: 'rgba(204,170,102,0.28)',  label: '雲母', filter: 'hue-rotate(35deg) saturate(1.4) brightness(1.1)' },
  sango:      { color: '#ee7766', glow: 'rgba(238,119,102,0.28)',  label: '珊瑚', filter: 'hue-rotate(10deg) saturate(1.8) brightness(1.05)' },
  shinju:     { color: '#f5f0e8', glow: 'rgba(245,240,232,0.28)',  label: '真珠', filter: 'saturate(0.1) brightness(1.7)' },
  menou:      { color: '#aa6633', glow: 'rgba(170,102,51,0.28)',   label: '瑪瑙', filter: 'hue-rotate(25deg) saturate(1.6) brightness(0.8)' },
  gecchou:    { color: '#aaccdd', glow: 'rgba(170,204,221,0.28)',  label: '月長', filter: 'hue-rotate(195deg) saturate(0.7) brightness(1.4)' },
  hotaruishi: { color: '#55cc77', glow: 'rgba(85,204,119,0.28)',   label: '蛍石', filter: 'hue-rotate(130deg) saturate(1.8) brightness(1.0)' },
  tekkou:     { color: '#667788', glow: 'rgba(102,119,136,0.28)',  label: '鉄鉱', filter: 'saturate(0.4) brightness(0.75)' },
  seigyoku:   { color: '#1144bb', glow: 'rgba(17,68,187,0.28)',    label: '青玉', filter: 'hue-rotate(215deg) saturate(2.5) brightness(0.7)' },
  kujaku:     { color: '#229988', glow: 'rgba(34,153,136,0.28)',   label: '孔雀', filter: 'hue-rotate(165deg) saturate(2.0) brightness(0.85)' },
  tanzanite:    { color: '#6B5B95', glow: 'rgba(107,91,149,0.28)',  label: 'タンザナイト', filter: 'hue-rotate(260deg) saturate(1.8) brightness(0.75)' },
  alexandrite:  { color: '#2E8B57', glow: 'rgba(46,139,87,0.28)',   label: 'アレキ', filter: 'hue-rotate(140deg) saturate(2.0) brightness(0.85)' },
  agate:        { color: '#4682B4', glow: 'rgba(70,130,180,0.28)',  label: 'アゲート', filter: 'hue-rotate(210deg) saturate(1.5) brightness(0.9)' },
  tourmaline:   { color: '#FF6B6B', glow: 'rgba(255,107,107,0.28)', label: 'トルマリン', filter: 'hue-rotate(0deg) saturate(2.0) brightness(1.0)' },
  citrine:      { color: '#FFD700', glow: 'rgba(255,215,0,0.28)',   label: 'シトリン', filter: 'hue-rotate(45deg) saturate(2.5) brightness(1.1)' },
  garnet:       { color: '#8B0000', glow: 'rgba(139,0,0,0.28)',     label: 'ガーネット', filter: 'hue-rotate(350deg) saturate(3.0) brightness(0.55)' },
  moonstone:    { color: '#B0C4DE', glow: 'rgba(176,196,222,0.28)', label: 'ムーンストーン', filter: 'saturate(0.3) brightness(1.5)' },
  opal:         { color: '#A8D8EA', glow: 'rgba(168,216,234,0.28)', label: 'オパール', filter: 'hue-rotate(190deg) saturate(0.8) brightness(1.4)' },
  labradorite:  { color: '#708090', glow: 'rgba(112,128,144,0.28)', label: 'ラブラドライト', filter: 'saturate(0.5) brightness(0.85)' }
};

// Load built-in sprite sheet PNG — split into individual canvas frames.
// Uses offscreen canvas (not createImageBitmap) for maximum browser compatibility.
async function _loadSpriteFrames(name, url) {
  try {
    var img = await new Promise(function(resolve, reject) {
      var i = new Image(); i.onload = function() { resolve(i); }; i.onerror = reject; i.src = url;
    });
    var fw = BUILTIN_SPRITE_FW, fh = BUILTIN_SPRITE_FH;
    var gem = GEM_TEAM[name];
    var needsTint = gem && gem.filter;
    var frames = [];
    for (var r = 0; r < BUILTIN_SPRITE_ROWS; r++) {
      for (var c = 0; c < BUILTIN_SPRITE_COLS; c++) {
        var fc = document.createElement('canvas');
        fc.width = fw; fc.height = fh;
        var fctx = fc.getContext('2d');
        if (needsTint) fctx.filter = gem.filter;
        fctx.drawImage(img, c * fw, r * fh, fw, fh, 0, 0, fw, fh);
        fctx.filter = 'none';
        frames.push(fc);
      }
    }
    SPRITE_FRAMES[name] = frames;
    console.log('[Sprites] ' + name + ': ' + frames.length + ' frames loaded (canvas-split)');
  } catch(e) {
    console.warn('[Sprites] Failed to load ' + name, e);
  }
}

(function() {
  var agents = [
    ['ruri', '/dashboard/sprites/ruri.png'],
    ['hisui', '/dashboard/sprites/hisui.png'],
    ['kokuyou', '/dashboard/sprites/kokuyou.png'],
    ['kohaku', '/dashboard/sprites/kohaku.png'],
    ['_default', '/dashboard/sprites/default.png']
  ];
  for (var i = 0; i < agents.length; i++) {
    _loadSpriteFrames(agents[i][0], agents[i][1]);
  }
})();

SpriteEngine.prototype._drawDefaultAgent = function(ctx, agent) {
  var fw = BUILTIN_SPRITE_FW, fh = BUILTIN_SPRITE_FH;
  var cols = BUILTIN_SPRITE_COLS;
  var gem = GEM_TEAM[agent.name];

  // Look up pre-split frame canvases (split at load time, no runtime clipping needed)
  var frames = SPRITE_FRAMES[agent.name] || SPRITE_FRAMES._default;

  var scale = 1.8;
  var dw = Math.round(fw * scale), dh = Math.round(fh * scale);
  var cx = Math.round(agent.x);
  var baseY = Math.round(agent.y);
  var dx = cx - Math.round(dw / 2);
  var dy = baseY - dh;
  var now = Date.now();

  // Idle bob
  if (agent.animState === 'idle') {
    dy += Math.round(Math.sin(now / 500) * 1.5);
  }

  // Gem aura glow (soft radial beneath character)
  if (gem) {
    var grad = ctx.createRadialGradient(cx, baseY + 1, 2, cx, baseY + 1, dw * 0.55);
    grad.addColorStop(0, gem.glow);
    grad.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = grad;
    ctx.beginPath();
    ctx.ellipse(cx, baseY + 1, dw * 0.55, 8, 0, 0, Math.PI * 2);
    ctx.fill();
  }

  // Shadow
  ctx.fillStyle = 'rgba(0,0,0,0.2)';
  ctx.beginPath();
  ctx.ellipse(cx, baseY + 2, dw * 0.35, 4, 0, 0, Math.PI * 2);
  ctx.fill();

  // Pick frame index: row 0 = idle/right, row 1 = walking/left
  var row = 0;
  var walking = agent.animState && agent.animState.indexOf('walk') === 0;
  if (walking || agent.facing === 'left') row = 1;
  var col = agent.frame % cols;
  var frameIdx = row * cols + col; // index into pre-split frames array

  // Error flash
  if (agent.animState === 'error' && Math.floor(now / 300) % 2 === 0) {
    ctx.globalAlpha = 0.5;
  }

  // Draw pre-split frame canvas (no source clipping — each frame is its own 32x32 canvas)
  if (frames && frames[frameIdx]) {
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(frames[frameIdx], dx, dy, dw, dh);
  } else {
    // Fallback: gem-colored pixel block
    ctx.fillStyle = gem ? gem.color : '#a78bfa';
    ctx.fillRect(dx + 8, dy + 8, dw - 16, dh - 8);
  }
  ctx.globalAlpha = 1;

  // Idle gem sparkle — subtle floating particles in gem color
  if (gem && agent.animState === 'idle') {
    var sparkT = now / 1200;
    ctx.globalAlpha = 0.4 + 0.3 * Math.sin(sparkT * 2);
    ctx.fillStyle = gem.color;
    ctx.fillRect(cx - 10 + Math.round(Math.sin(sparkT) * 8), dy + 4 + Math.round(Math.cos(sparkT * 0.7) * 12), 2, 2);
    ctx.fillRect(cx + 6 + Math.round(Math.cos(sparkT * 1.3) * 6), dy + 10 + Math.round(Math.sin(sparkT * 0.9) * 8), 2, 2);
    ctx.globalAlpha = 1;
  }

  // Work indicator: thought dots above head
  if (agent.animState === 'work' || agent.animState === 'think') {
    ctx.fillStyle = gem ? gem.color : '#fbbf24';
    var dotP = Math.floor(now / 400) % 4;
    for (var di = 0; di < Math.min(dotP, 3); di++) {
      ctx.fillRect(cx + dw / 2 + di * 4, dy - 4 - di * 3, 3, 3);
    }
  }

  // Celebrate sparkles — gem-colored burst
  if (agent.animState === 'celebrate') {
    var sp = now / 200;
    var sparkColors = gem
      ? [gem.color, '#ffffff', gem.color, '#fbbf24']
      : ['#fbbf24', '#f87171', '#34d399', '#60a5fa'];
    for (var si = 0; si < 6; si++) {
      ctx.fillStyle = sparkColors[si % sparkColors.length];
      ctx.globalAlpha = 0.6 + 0.4 * Math.sin(sp + si);
      ctx.fillRect(
        cx - 14 + Math.round(Math.sin(sp + si * 1.3) * 20),
        dy - 8 + Math.round(Math.cos(sp + si * 1.7) * 14), 3, 3);
    }
    ctx.globalAlpha = 1;
  }

  // Error exclamation
  if (agent.animState === 'error') {
    ctx.fillStyle = '#f87171';
    ctx.fillRect(cx + dw / 2 + 2, dy, 4, 8);
    ctx.fillRect(cx + dw / 2 + 2, dy + 10, 4, 3);
  }

  // Flash highlight ring (minimap click) — gem colored
  if (agent._flashTimer > 0) {
    var fa = 0.3 + 0.7 * Math.abs(Math.sin(now / 150));
    ctx.globalAlpha = fa;
    ctx.strokeStyle = gem ? gem.color : '#ffffff';
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.ellipse(cx, baseY - dh / 2, dw / 2 + 4, dh / 2 + 4, 0, 0, Math.PI * 2);
    ctx.stroke();
    ctx.globalAlpha = 1;
  }

  // Name label — gem-colored text on dark bg
  ctx.font = 'bold 9px monospace';
  ctx.textAlign = 'center';
  var nameW = ctx.measureText(agent.name).width;
  ctx.fillStyle = 'rgba(0,0,0,0.55)';
  var labelX = cx - nameW / 2 - 4;
  var labelY = baseY + 4;
  var labelW = nameW + 8;
  var labelH = 13;
  ctx.beginPath();
  ctx.moveTo(labelX + 3, labelY);
  ctx.lineTo(labelX + labelW - 3, labelY);
  ctx.quadraticCurveTo(labelX + labelW, labelY, labelX + labelW, labelY + 3);
  ctx.lineTo(labelX + labelW, labelY + labelH - 3);
  ctx.quadraticCurveTo(labelX + labelW, labelY + labelH, labelX + labelW - 3, labelY + labelH);
  ctx.lineTo(labelX + 3, labelY + labelH);
  ctx.quadraticCurveTo(labelX, labelY + labelH, labelX, labelY + labelH - 3);
  ctx.lineTo(labelX, labelY + 3);
  ctx.quadraticCurveTo(labelX, labelY, labelX + 3, labelY);
  ctx.fill();
  if (gem) {
    ctx.strokeStyle = gem.color;
    ctx.lineWidth = 1;
    ctx.globalAlpha = 0.5;
    ctx.stroke();
    ctx.globalAlpha = 1;
  }
  ctx.fillStyle = gem ? gem.color : '#e8e8e8';
  ctx.fillText(agent.name, cx, baseY + 14);
  ctx.textAlign = 'start';
};

function _darkenColor(hex, amount) {
  var r = parseInt(hex.slice(1, 3), 16);
  var g = parseInt(hex.slice(3, 5), 16);
  var b = parseInt(hex.slice(5, 7), 16);
  r = Math.round(r * (1 - amount));
  g = Math.round(g * (1 - amount));
  b = Math.round(b * (1 - amount));
  return '#' + ((1 << 24) + (r << 16) + (g << 8) + b).toString(16).slice(1);
}

SpriteEngine.prototype._drawAgent = function(ctx, agent) {
  if (!this.config) return;
  var cw = this.config.cellWidth || 32;
  var ch = this.config.cellHeight || 32;
  var stDef = this.stateMap[agent.animState];
  if (!stDef) stDef = this.stateMap['idle'] || { row: 0, frames: 4 };

  // Use pre-split frames (createImageBitmap at load time — no runtime source clipping)
  var split = this.splitFrames[agent.name];
  var frame = null;
  if (split) {
    // Multi-sheet: per-state frame arrays
    if (split.perState) {
      var stFrames = split.perState[agent.animState] || split.perState['idle'];
      if (stFrames && stFrames.length > 0) {
        frame = stFrames[agent.frame % stFrames.length];
      }
    }
    // Single-sheet: row * cols + col
    if (!frame && split.frames && split.frames.length > 0) {
      var row = Math.min(stDef.row, split.rows - 1);
      if (row < 0) row = 0;
      var col = agent.frame % Math.min(stDef.frames, split.cols);
      var idx = row * split.cols + col;
      if (idx < split.frames.length) frame = split.frames[idx];
    }
  }

  // No pre-split frames — fall back to built-in sprites
  if (!frame) {
    this._drawDefaultAgent(ctx, agent);
    return;
  }

  // Scale up for 640x360 canvas
  var scale = 1.8;
  var dw = Math.round(cw * scale), dh = Math.round(ch * scale);
  var cx = Math.round(agent.x);
  var baseY = Math.round(agent.y);
  var dx = cx - Math.round(dw / 2);
  var dy = baseY - dh;
  var now = Date.now();
  var gem = GEM_TEAM[agent.name];

  // Idle bob
  if (agent.animState === 'idle') dy += Math.round(Math.sin(now / 500) * 1.5);

  // Gem aura glow
  if (gem) {
    var grad = ctx.createRadialGradient(cx, baseY + 1, 2, cx, baseY + 1, dw * 0.55);
    grad.addColorStop(0, gem.glow);
    grad.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = grad;
    ctx.beginPath();
    ctx.ellipse(cx, baseY + 1, dw * 0.55, 8, 0, 0, Math.PI * 2);
    ctx.fill();
  }

  // Shadow
  ctx.fillStyle = 'rgba(0,0,0,0.2)';
  ctx.beginPath();
  ctx.ellipse(cx, baseY + 2, dw * 0.35, 4, 0, 0, Math.PI * 2);
  ctx.fill();

  // Error flash
  if (agent.animState === 'error' && Math.floor(now / 300) % 2 === 0) ctx.globalAlpha = 0.5;

  // Draw single pre-split frame — simple 5-param drawImage, NO source clipping
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(frame, dx, dy, dw, dh);
  ctx.globalAlpha = 1;

  // Effects (same as _drawDefaultAgent)
  if (gem && agent.animState === 'idle') {
    var sparkT = now / 1200;
    ctx.globalAlpha = 0.4 + 0.3 * Math.sin(sparkT * 2);
    ctx.fillStyle = gem.color;
    ctx.fillRect(cx - 10 + Math.round(Math.sin(sparkT) * 8), dy + 4 + Math.round(Math.cos(sparkT * 0.7) * 12), 2, 2);
    ctx.fillRect(cx + 6 + Math.round(Math.cos(sparkT * 1.3) * 6), dy + 10 + Math.round(Math.sin(sparkT * 0.9) * 8), 2, 2);
    ctx.globalAlpha = 1;
  }
  if (agent.animState === 'work' || agent.animState === 'think') {
    ctx.fillStyle = gem ? gem.color : '#fbbf24';
    var dotP = Math.floor(now / 400) % 4;
    for (var di = 0; di < Math.min(dotP, 3); di++) ctx.fillRect(cx + dw / 2 + di * 4, dy - 4 - di * 3, 3, 3);
  }
  if (agent.animState === 'celebrate') {
    var sp = now / 200;
    var sparkColors = gem ? [gem.color, '#fff', gem.color, '#fbbf24'] : ['#fbbf24', '#f87171', '#34d399', '#60a5fa'];
    for (var si = 0; si < 6; si++) {
      ctx.fillStyle = sparkColors[si % sparkColors.length];
      ctx.globalAlpha = 0.6 + 0.4 * Math.sin(sp + si);
      ctx.fillRect(cx - 14 + Math.round(Math.sin(sp + si * 1.3) * 20), dy - 8 + Math.round(Math.cos(sp + si * 1.7) * 14), 3, 3);
    }
    ctx.globalAlpha = 1;
  }
  if (agent.animState === 'error') {
    ctx.fillStyle = '#f87171';
    ctx.fillRect(cx + dw / 2 + 2, dy, 4, 8);
    ctx.fillRect(cx + dw / 2 + 2, dy + 10, 4, 3);
  }
  if (agent._flashTimer > 0) {
    ctx.globalAlpha = 0.3 + 0.7 * Math.abs(Math.sin(now / 150));
    ctx.strokeStyle = gem ? gem.color : '#fff';
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.ellipse(cx, baseY - dh / 2, dw / 2 + 4, dh / 2 + 4, 0, 0, Math.PI * 2);
    ctx.stroke();
    ctx.globalAlpha = 1;
  }

  // Name label
  ctx.font = 'bold 9px monospace';
  ctx.textAlign = 'center';
  var nameW = ctx.measureText(agent.name).width;
  ctx.fillStyle = 'rgba(0,0,0,0.55)';
  var lx = cx - nameW / 2 - 4, ly = baseY + 4, lw = nameW + 8, lh = 13;
  ctx.beginPath();
  ctx.moveTo(lx + 3, ly); ctx.lineTo(lx + lw - 3, ly);
  ctx.quadraticCurveTo(lx + lw, ly, lx + lw, ly + 3); ctx.lineTo(lx + lw, ly + lh - 3);
  ctx.quadraticCurveTo(lx + lw, ly + lh, lx + lw - 3, ly + lh); ctx.lineTo(lx + 3, ly + lh);
  ctx.quadraticCurveTo(lx, ly + lh, lx, ly + lh - 3); ctx.lineTo(lx, ly + 3);
  ctx.quadraticCurveTo(lx, ly, lx + 3, ly);
  ctx.fill();
  if (gem) { ctx.strokeStyle = gem.color; ctx.lineWidth = 1; ctx.globalAlpha = 0.5; ctx.stroke(); ctx.globalAlpha = 1; }
  ctx.fillStyle = gem ? gem.color : '#e8e8e8';
  ctx.fillText(agent.name, cx, baseY + 14);
  ctx.textAlign = 'start';
};

// --- Office Decoration System ---
var DECOR_CATALOG = {
  plant:     { w: 16, h: 20, label: 'Plant' },
  bookshelf: { w: 24, h: 20, label: 'Books' },
  lamp:      { w: 8,  h: 20, label: 'Lamp' },
  poster:    { w: 20, h: 16, label: 'Poster' },
  clock:     { w: 12, h: 12, label: 'Clock' },
  cactus:    { w: 10, h: 14, label: 'Cactus' },
  monitor:   { w: 18, h: 16, label: 'Monitor' },
  cat:       { w: 12, h: 10, label: 'Cat' },
  coffee:    { w: 8,  h: 8,  label: 'Coffee' },
  rug:       { w: 32, h: 16, label: 'Rug' }
};

var _decorations = [];
var _decorEditMode = false;
var _decorDragging = null; // { idx, offX, offY }
var _decorSelected = -1;

// Load decorations from localStorage
(function() {
  try {
    var saved = localStorage.getItem('tetora-decorations');
    if (saved) _decorations = JSON.parse(saved);
  } catch(e) {}
})();

function _saveDecorations() {
  try { localStorage.setItem('tetora-decorations', JSON.stringify(_decorations)); } catch(e) {}
}

// Pixel-art draw functions for each decoration type
var DECOR_DRAW = {
  plant: function(ctx, x, y) {
    ctx.fillStyle = '#5a3a1a'; ctx.fillRect(x+5, y+14, 6, 6); // pot
    ctx.fillStyle = '#2d8a4e'; ctx.fillRect(x+4, y+6, 8, 8);  // leaves
    ctx.fillStyle = '#1a6b3a'; ctx.fillRect(x+6, y+2, 4, 6);  // top
    ctx.fillStyle = '#3aaa5e'; ctx.fillRect(x+2, y+8, 4, 4);  // left leaf
    ctx.fillRect(x+10, y+8, 4, 4); // right leaf
  },
  bookshelf: function(ctx, x, y) {
    ctx.fillStyle = '#4a3520'; ctx.fillRect(x, y, 24, 20);     // frame
    ctx.fillStyle = '#3a2510'; ctx.fillRect(x+1, y+1, 22, 8);  // shelf top
    ctx.fillRect(x+1, y+11, 22, 8); // shelf bot
    ctx.fillStyle = '#e74c3c'; ctx.fillRect(x+2, y+2, 4, 6);   // red book
    ctx.fillStyle = '#3498db'; ctx.fillRect(x+7, y+2, 4, 6);   // blue book
    ctx.fillStyle = '#f1c40f'; ctx.fillRect(x+12, y+3, 3, 5);  // yellow book
    ctx.fillStyle = '#2ecc71'; ctx.fillRect(x+16, y+2, 5, 6);  // green book
    ctx.fillStyle = '#9b59b6'; ctx.fillRect(x+3, y+12, 4, 6);  // purple book
    ctx.fillStyle = '#e67e22'; ctx.fillRect(x+8, y+12, 5, 6);  // orange book
    ctx.fillStyle = '#1abc9c'; ctx.fillRect(x+14, y+13, 4, 5); // teal book
  },
  lamp: function(ctx, x, y) {
    ctx.fillStyle = '#fbbf24'; ctx.fillRect(x+1, y, 6, 6);     // shade
    ctx.fillStyle = '#e5a91a'; ctx.fillRect(x+2, y+6, 4, 2);   // rim
    ctx.fillStyle = '#888'; ctx.fillRect(x+3, y+8, 2, 10);     // pole
    ctx.fillStyle = '#666'; ctx.fillRect(x+1, y+18, 6, 2);     // base
  },
  poster: function(ctx, x, y) {
    ctx.fillStyle = '#2a2a4a'; ctx.fillRect(x, y, 20, 16);     // frame
    ctx.fillStyle = '#1a1a3a'; ctx.fillRect(x+1, y+1, 18, 14); // bg
    ctx.fillStyle = '#f87171'; ctx.fillRect(x+3, y+3, 6, 4);   // art
    ctx.fillStyle = '#60a5fa'; ctx.fillRect(x+11, y+3, 6, 4);
    ctx.fillStyle = '#fbbf24'; ctx.fillRect(x+5, y+9, 10, 2);  // text line
    ctx.fillStyle = '#888'; ctx.fillRect(x+7, y+12, 6, 1);
  },
  clock: function(ctx, x, y) {
    ctx.fillStyle = '#3a3a5a'; ctx.fillRect(x+1, y, 10, 12);   // body
    ctx.fillStyle = '#1a1a2a'; ctx.fillRect(x+2, y+1, 8, 8);   // face
    ctx.fillStyle = '#ffffff'; ctx.fillRect(x+5, y+2, 2, 4);    // hour hand
    ctx.fillRect(x+5, y+4, 4, 2);  // minute hand
    ctx.fillStyle = '#f87171'; ctx.fillRect(x+5, y+4, 2, 2);    // center
    ctx.fillStyle = '#fbbf24'; ctx.fillRect(x+3, y+10, 6, 2);   // pendulum
  },
  cactus: function(ctx, x, y) {
    ctx.fillStyle = '#5a3a1a'; ctx.fillRect(x+2, y+10, 6, 4);  // pot
    ctx.fillStyle = '#2d8a4e'; ctx.fillRect(x+3, y+2, 4, 10);  // body
    ctx.fillStyle = '#3aaa5e'; ctx.fillRect(x, y+4, 3, 4);      // left arm
    ctx.fillRect(x+7, y+3, 3, 4);   // right arm
    ctx.fillStyle = '#f87171'; ctx.fillRect(x+4, y, 2, 2);      // flower
  },
  monitor: function(ctx, x, y) {
    ctx.fillStyle = '#333'; ctx.fillRect(x, y, 18, 12);         // frame
    ctx.fillStyle = '#0a0a2a'; ctx.fillRect(x+1, y+1, 16, 9);  // screen
    ctx.fillStyle = '#60a5fa'; ctx.fillRect(x+2, y+2, 8, 1);   // text
    ctx.fillStyle = '#4ade80'; ctx.fillRect(x+2, y+4, 12, 1);
    ctx.fillStyle = '#f87171'; ctx.fillRect(x+2, y+6, 6, 1);
    ctx.fillStyle = '#888'; ctx.fillRect(x+7, y+12, 4, 2);     // stand
    ctx.fillRect(x+5, y+14, 8, 2);
  },
  cat: function(ctx, x, y) {
    ctx.fillStyle = '#f5a623'; ctx.fillRect(x+2, y+2, 8, 6);   // body
    ctx.fillStyle = '#e5961a'; ctx.fillRect(x+1, y, 3, 3);      // left ear
    ctx.fillRect(x+8, y, 3, 3);     // right ear
    ctx.fillStyle = '#ffffff'; ctx.fillRect(x+3, y+3, 2, 2);    // left eye
    ctx.fillRect(x+7, y+3, 2, 2);   // right eye
    ctx.fillStyle = '#333'; ctx.fillRect(x+4, y+3, 1, 1);       // pupil L
    ctx.fillRect(x+8, y+3, 1, 1);   // pupil R
    ctx.fillStyle = '#f87171'; ctx.fillRect(x+5, y+5, 2, 1);    // nose
    ctx.fillStyle = '#e5961a'; ctx.fillRect(x+10, y+4, 2, 2);   // tail
  },
  coffee: function(ctx, x, y) {
    ctx.fillStyle = '#ffffff'; ctx.fillRect(x+1, y+2, 6, 6);    // cup
    ctx.fillStyle = '#5a3a1a'; ctx.fillRect(x+2, y+3, 4, 4);   // coffee
    ctx.fillStyle = '#ffffff'; ctx.fillRect(x+6, y+3, 2, 2);    // handle
    ctx.fillStyle = '#888'; ctx.fillRect(x, y+7, 8, 1);         // saucer
    // Steam
    var st = Math.floor(Date.now() / 300) % 3;
    ctx.fillStyle = 'rgba(255,255,255,0.4)';
    ctx.fillRect(x+2+st, y, 1, 2);
    ctx.fillRect(x+5-st, y+1, 1, 1);
  },
  rug: function(ctx, x, y) {
    ctx.fillStyle = '#8b2252'; ctx.fillRect(x, y+2, 32, 12);    // base
    ctx.fillStyle = '#a0305e'; ctx.fillRect(x+2, y+4, 28, 8);   // inner
    ctx.fillStyle = '#fbbf24'; ctx.fillRect(x+4, y+6, 24, 4);   // center stripe
    ctx.fillStyle = '#8b2252'; ctx.fillRect(x+8, y+7, 16, 2);   // pattern
    // Fringe
    ctx.fillStyle = '#a0305e';
    for (var fi = 0; fi < 8; fi++) {
      ctx.fillRect(x + 2 + fi * 4, y, 2, 2);
      ctx.fillRect(x + 2 + fi * 4, y + 14, 2, 2);
    }
  }
};

function openSpriteGuide() {
  document.getElementById('sprite-guide-modal').classList.add('open');
}
function closeSpriteGuide() {
  document.getElementById('sprite-guide-modal').classList.remove('open');
}

// Office zoom
var _officeZoom = parseInt(localStorage.getItem('tetora-office-zoom')) || 1;
function officeZoom(dir) {
  _officeZoom = Math.max(1, Math.min(3, _officeZoom + dir));
  var cv = document.getElementById('agent-world-cv');
  cv.style.transform = _officeZoom > 1 ? 'scale('+_officeZoom+')' : '';
  cv.style.transformOrigin = 'top left';
  var ctr = document.getElementById('agent-world-container');
  if (_officeZoom > 1) { ctr.style.overflow = 'auto'; } else { ctr.style.overflow = ''; }
  document.getElementById('aw-zoom-label').textContent = _officeZoom + 'x';
  localStorage.setItem('tetora-office-zoom', _officeZoom);
}
// Apply saved zoom on load
(function() { if (_officeZoom > 1) setTimeout(function() { officeZoom(0); }, 500); })();

function toggleDecorEditMode() {
  _decorEditMode = !_decorEditMode;
  _decorSelected = -1;
  var btn = document.getElementById('aw-edit-btn');
  var palette = document.getElementById('decor-palette');
  if (_decorEditMode) {
    btn.textContent = 'Done';
    btn.classList.add('active');
    palette.style.display = 'flex';
    // Populate palette if empty
    if (!palette.dataset.built) {
      for (var type in DECOR_CATALOG) {
        var item = document.createElement('canvas');
        item.className = 'decor-palette-item';
        item.width = 32; item.height = 32;
        item.title = DECOR_CATALOG[type].label;
        item.dataset.type = type;
        var pctx = item.getContext('2d');
        var cat = DECOR_CATALOG[type];
        var ox = Math.round((32 - cat.w) / 2);
        var oy = Math.round((32 - cat.h) / 2);
        if (DECOR_DRAW[type]) DECOR_DRAW[type](pctx, ox, oy);
        item.addEventListener('click', (function(t) {
          return function(e) { e.stopPropagation(); addDecoration(t); };
        })(type));
        palette.appendChild(item);
      }
      // Export/Import buttons
      var sep = document.createElement('div');
      sep.style.cssText = 'width:1px;background:var(--border);flex-shrink:0;margin:0 4px';
      palette.appendChild(sep);
      var expBtn = document.createElement('button');
      expBtn.className = 'btn';
      expBtn.style.cssText = 'padding:4px 8px;font-size:10px;white-space:nowrap;flex-shrink:0';
      expBtn.textContent = 'Export';
      expBtn.onclick = function(e) { e.stopPropagation(); exportOfficeLayout(); };
      palette.appendChild(expBtn);
      var impBtn = document.createElement('button');
      impBtn.className = 'btn';
      impBtn.style.cssText = 'padding:4px 8px;font-size:10px;white-space:nowrap;flex-shrink:0';
      impBtn.textContent = 'Import';
      impBtn.onclick = function(e) { e.stopPropagation(); importOfficeLayout(); };
      palette.appendChild(impBtn);
      palette.dataset.built = '1';
    }
  } else {
    btn.textContent = 'Edit';
    btn.classList.remove('active');
    palette.style.display = 'none';
  }
}

function addDecoration(type) {
  _decorations.push({ type: type, x: 300 - DECOR_CATALOG[type].w/2, y: 100 - DECOR_CATALOG[type].h/2 });
  _saveDecorations();
}

function exportOfficeLayout() {
  var data = JSON.stringify(_decorations, null, 2);
  var blob = new Blob([data], { type: 'application/json' });
  var a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = 'tetora-office-layout.json';
  a.click();
  URL.revokeObjectURL(a.href);
}

function importOfficeLayout() {
  var inp = document.createElement('input');
  inp.type = 'file';
  inp.accept = '.json';
  inp.onchange = function() {
    var file = inp.files[0];
    if (!file) return;
    var reader = new FileReader();
    reader.onload = function() {
      try {
        var parsed = JSON.parse(reader.result);
        if (!Array.isArray(parsed)) throw new Error('invalid');
        _decorations = parsed;
        _saveDecorations();
        addNotification('Office layout imported (' + parsed.length + ' items)', 'success');
      } catch(e) {
        addNotification('Import failed: invalid JSON', 'error');
      }
    };
    reader.readAsText(file);
  };
  inp.click();
}

function _canvasCoords(e) {
  var cv = document.getElementById('agent-world-cv');
  if (!cv) return { x: 0, y: 0 };
  var rect = cv.getBoundingClientRect();
  return {
    x: (e.clientX - rect.left) * (OFFICE.W / rect.width),
    y: (e.clientY - rect.top) * (OFFICE.H / rect.height)
  };
}

// Draw decorations — called in render between office and agents
SpriteEngine.prototype._drawDecorations = function(ctx) {
  for (var i = 0; i < _decorations.length; i++) {
    var d = _decorations[i];
    if (DECOR_DRAW[d.type]) DECOR_DRAW[d.type](ctx, d.x, d.y);
  }
};

// Draw edit overlay (selection highlight)
SpriteEngine.prototype._drawDecorEditOverlay = function(ctx) {
  if (!_decorEditMode) return;
  for (var i = 0; i < _decorations.length; i++) {
    var d = _decorations[i];
    var cat = DECOR_CATALOG[d.type];
    if (!cat) continue;
    ctx.strokeStyle = i === _decorSelected ? '#fbbf24' : 'rgba(255,255,255,0.2)';
    ctx.lineWidth = 1;
    ctx.setLineDash(i === _decorSelected ? [] : [2, 2]);
    ctx.strokeRect(d.x - 1, d.y - 1, cat.w + 2, cat.h + 2);
  }
  ctx.setLineDash([]);
};

// Decoration drag handlers
(function() {
  var cv = document.getElementById('agent-world-cv');
  if (!cv) return;

  cv.addEventListener('mousedown', function(e) {
    if (!_decorEditMode) return;
    var p = _canvasCoords(e);
    // Hit-test decorations in reverse order (top-most first)
    for (var i = _decorations.length - 1; i >= 0; i--) {
      var d = _decorations[i];
      var cat = DECOR_CATALOG[d.type];
      if (p.x >= d.x && p.x <= d.x + cat.w && p.y >= d.y && p.y <= d.y + cat.h) {
        _decorDragging = { idx: i, offX: p.x - d.x, offY: p.y - d.y };
        _decorSelected = i;
        e.preventDefault();
        return;
      }
    }
    _decorSelected = -1;
  });

  cv.addEventListener('mousemove', function(e) {
    if (!_decorDragging) return;
    var p = _canvasCoords(e);
    var d = _decorations[_decorDragging.idx];
    d.x = p.x - _decorDragging.offX;
    d.y = p.y - _decorDragging.offY;
  });

  cv.addEventListener('mouseup', function(_e) {
    if (!_decorDragging) return;
    var d = _decorations[_decorDragging.idx];
    var cat = DECOR_CATALOG[d.type];
    // Clamp to canvas bounds
    d.x = Math.max(0, Math.min(OFFICE.W - cat.w, Math.round(d.x)));
    d.y = Math.max(0, Math.min(OFFICE.H - cat.h, Math.round(d.y)));
    _decorDragging = null;
    _saveDecorations();
  });

  // Right-click to delete in edit mode
  cv.addEventListener('contextmenu', function(e) {
    if (!_decorEditMode) return;
    e.preventDefault();
    var p = _canvasCoords(e);
    for (var i = _decorations.length - 1; i >= 0; i--) {
      var d = _decorations[i];
      var cat = DECOR_CATALOG[d.type];
      if (p.x >= d.x && p.x <= d.x + cat.w && p.y >= d.y && p.y <= d.y + cat.h) {
        _decorations.splice(i, 1);
        _decorSelected = -1;
        _saveDecorations();
        return;
      }
    }
  });
})();

// --- Agent Click Interaction ---
(function() {
  var cv = document.getElementById('agent-world-cv');
  if (!cv) return;
  cv.addEventListener('click', function(e) {
    if (_decorEditMode) return; // bypass agent clicks in edit mode
    if (!spriteEngine || !spriteEngine.agents) return;
    var rect = cv.getBoundingClientRect();
    var scaleX = OFFICE.W / rect.width;
    var scaleY = OFFICE.H / rect.height;
    var mx = (e.clientX - rect.left) * scaleX;
    var my = (e.clientY - rect.top) * scaleY;
    var cw = (spriteEngine.config && spriteEngine.config.cellWidth) || 32;
    var ch = (spriteEngine.config && spriteEngine.config.cellHeight) || 32;
    for (var name in spriteEngine.agents) {
      var a = spriteEngine.agents[name];
      if (mx >= a.x - cw/2 && mx <= a.x + cw/2 && my >= a.y - ch && my <= a.y + 4) {
        showAgentInfoModal(a);
        return;
      }
    }
  });
})();

function showAgentInfoModal(agent) {
  var name = agent.name;
  var role = window._cachedRoles && window._cachedRoles[name];
  var modal = document.createElement('div');
  modal.className = 'modal-overlay open';
  modal.onclick = function(e) { if (e.target === modal) document.body.removeChild(modal); };
  var desc = role ? (role.description || '') : '';
  var model = role ? (role.model || '') : '';
  var color = AGENT_COLORS[name] || DEFAULT_AGENT_COLOR;
  modal.innerHTML = '<div class="modal" style="min-width:320px">' +
    '<button class="modal-close" onclick="this.closest(\'.modal-overlay\').remove()">&times;</button>' +
    '<h3 style="color:' + color + '">' + name + '</h3>' +
    '<div style="margin:12px 0;font-size:14px">' +
    '<div><span style="color:var(--muted)">Status:</span> ' + (agent.spriteState || 'idle') + '</div>' +
    (model ? '<div><span style="color:var(--muted)">Model:</span> ' + model + '</div>' : '') +
    (desc ? '<div style="margin-top:8px;color:var(--muted);font-size:13px">' + desc + '</div>' : '') +
    '</div></div>';
  document.body.appendChild(modal);
  play8BitSound('select');
}

// --- 8-Bit Sound Effects (Web Audio API, opt-in) ---
var _audioCtx = null;
var _soundEnabled = localStorage.getItem('tetora-sound') === 'true';
function play8BitSound(type) {
  if (!_soundEnabled) return;
  // @ts-ignore: webkitAudioContext is a legacy vendor-prefixed fallback
  if (!_audioCtx) { try { _audioCtx = new (window.AudioContext || window.webkitAudioContext)(); } catch(e) { return; } }
  var ctx = _audioCtx;
  var osc = ctx.createOscillator();
  var gain = ctx.createGain();
  osc.connect(gain);
  gain.connect(ctx.destination);
  gain.gain.value = 0.08;
  if (type === 'complete') {
    osc.type = 'square'; osc.frequency.setValueAtTime(523, ctx.currentTime);
    osc.frequency.setValueAtTime(659, ctx.currentTime + 0.1); osc.frequency.setValueAtTime(784, ctx.currentTime + 0.2);
    gain.gain.setValueAtTime(0.08, ctx.currentTime + 0.25); gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.35);
    osc.start(ctx.currentTime); osc.stop(ctx.currentTime + 0.35);
  } else if (type === 'error') {
    osc.type = 'square'; osc.frequency.setValueAtTime(200, ctx.currentTime);
    osc.frequency.setValueAtTime(150, ctx.currentTime + 0.15);
    gain.gain.setValueAtTime(0.06, ctx.currentTime + 0.25); gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.3);
    osc.start(ctx.currentTime); osc.stop(ctx.currentTime + 0.3);
  } else if (type === 'select') {
    osc.type = 'square'; osc.frequency.setValueAtTime(440, ctx.currentTime);
    gain.gain.setValueAtTime(0.05, ctx.currentTime); gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.08);
    osc.start(ctx.currentTime); osc.stop(ctx.currentTime + 0.08);
  } else if (type === 'coin') {
    osc.type = 'square'; osc.frequency.setValueAtTime(988, ctx.currentTime);
    osc.frequency.setValueAtTime(1319, ctx.currentTime + 0.08);
    gain.gain.setValueAtTime(0.06, ctx.currentTime + 0.15); gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.2);
    osc.start(ctx.currentTime); osc.stop(ctx.currentTime + 0.2);
  }
}
// --- End Agent World ---

pollTimer = setInterval(refresh, 5000);
connectDashboardSSE();
startHumanGatePolling();
document.addEventListener('visibilitychange', function() {
  if (document.hidden) {
    clearInterval(pollTimer);
    if (dashboardSSE) { dashboardSSE.close(); dashboardSSE = null; sseConnected = false; updateSSEBadge(); }
    // Pause sprite engine when tab is hidden.
    if (typeof spriteEngine !== 'undefined' && spriteEngine && spriteEngine.running) spriteEngine.stop();
  } else {
    refresh();
    if (currentTab === 'dashboard') refreshWorkers();
    pollTimer = setInterval(refresh, sseConnected ? 15000 : 5000);
    connectDashboardSSE();
    // Resume sprite engine if agent world was open.
    if (typeof agentWorldOpen !== 'undefined' && agentWorldOpen && typeof spriteEngine !== 'undefined' && spriteEngine) spriteEngine.start();
  }
});

