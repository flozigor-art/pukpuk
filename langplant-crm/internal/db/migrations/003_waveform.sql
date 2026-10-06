-- Waveforms are now 1000 loudness (RMS) bins instead of 200 peak bins: ask
-- the storage node to recompute them for audio it already has.
UPDATE blobs SET derive_state = ''
WHERE mime LIKE 'audio/%' AND state = 'stored' AND derive_state = 'done'
  AND (peaks IS NULL OR json_array_length(peaks) < 1000);
