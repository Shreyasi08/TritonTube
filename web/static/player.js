'use strict';
const video=document.querySelector('#player'),error=document.querySelector('#player-error');
if(!window.dashjs){error.textContent='The DASH player could not load. Check your internet connection and reload.';}else{const player=dashjs.MediaPlayer().create();player.initialize(video,video.dataset.manifest,false);player.on(dashjs.MediaPlayer.events.ERROR,()=>{error.textContent='Playback encountered an error. Check cluster health and try reloading.';});}
