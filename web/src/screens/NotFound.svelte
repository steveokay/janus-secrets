<script lang="ts">
  import Guilloche from '../components/Guilloche.svelte'
</script>

<div class="nf">
  <div class="rosette" aria-hidden="true"><Guilloche size={360} rings={14} opacity={0.12} /></div>
  <p class="folio">FOL. 404</p>
  <h1>No such folio.</h1>
  <p class="sub">This page is not on record. <a href="/">Return to the registry.</a></p>
</div>

<style>
  .nf {
    position: relative;
    text-align: center;
    padding: var(--s8) var(--s5);
  }
  .rosette {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    color: var(--ink);
    pointer-events: none;
  }
  h1 { margin: var(--s2) 0 var(--s3); }
  .sub { color: var(--ink-soft); }
</style>
