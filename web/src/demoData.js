const now = new Date('2026-06-06T12:00:00+09:00').toISOString();
const streamID = 'stream-demo-discord-audio';
const incompleteStreamID = 'stream-demo-external-e2e-incomplete';
const incidentID = 'incident-discord-forward-stale';
const voiceDisconnectIncidentID = 'incident-discord-voice-disconnected';

const discordForwardReport = {
  summary: 'Discord 音声 packet の Encoder/Recorder 転送が停滞しています。',
  likely_cause: 'Discord Bot は起動していますが、Encoder/Recorder への forward が進んでいません。Encoder public URL、service token、network 到達性を確認してください。',
  confidence: 0.74,
  evidence: [
    'discord.audio_last_forward_age_sec=12',
    'discord.audio_forwarded_total=120',
    'discord.audio_forward_errors_total=4',
  ],
  impact: '配信音声とアーカイブ音声が欠落する可能性があります。',
  recommended_actions: [
    'discord.audio_last_forward_age_sec と discord.audio_forwarded_total を確認する',
    'Encoder/Recorder の /streams/{id}/audio-status を確認する',
    'Bot から Encoder/Recorder public URL への到達性を確認する',
  ],
  safe_auto_remediation_candidates: ['refresh_service_status', 'rerun_diagnostics'],
  actions_requiring_approval: ['restart_discord_bot', 'restart_encoder_recorder'],
};

const discordVoiceDisconnectReport = {
  summary: 'Discord Bot が配信中の voice channel から切断されました。',
  likely_cause: 'Bot が手動で VC から移動・切断された、Discord voice connection が切断された、権限変更や network 瞬断が発生した可能性があります。',
  confidence: 0.82,
  evidence: [
    'discord.voice_disconnect_count=1',
    'discord.voice_connected=0',
    'discord.audio_forward_active=0',
  ],
  impact: '配信中の音声入力が止まり、Encoder/Recorder への audio forward と caption/STT 入力が欠落する可能性があります。',
  recommended_actions: [
    'discord.voice_disconnect_count と discord.voice_connected を確認する',
    'Bot が正しい voice channel に残っているか確認する',
    'Encoder/Recorder の /streams/{id}/audio-status で packet 到達を確認する',
  ],
  safe_auto_remediation_candidates: ['refresh_service_status', 'rerun_diagnostics'],
  actions_requiring_approval: ['reconnect_discord_voice', 'restart_discord_bot'],
};

export const demoAPIEnabled = import.meta.env.VITE_AUTOSTREAM_DEMO === 'true';

export function demoAPIData(path, shape = 'array') {
  if (!demoAPIEnabled) return null;
  const normalizedPath = stripQuery(path);
  const byPath = {
    '/auth/me': { user: { id: 'user-demo', username: 'demo-admin', roles: ['super_admin'] } },
    '/streams': [
      {
        id: streamID,
        name: 'Demo Discord Audio Stream',
        status: 'live',
        discord_config_id: 'discord-config-demo',
        youtube_output_id: 'youtube-output-demo',
        archive_profile_id: 'archive-profile-demo',
        created_at: now,
        updated_at: now,
      },
      {
        id: incompleteStreamID,
        name: 'External E2E Setup Checklist',
        status: 'draft',
        created_at: now,
        updated_at: now,
      },
    ],
    '/service-health': [
      {
        service_id: 'discord-bot-demo',
        service_type: 'discord_bot',
        service_name: 'Discord Bot Demo',
        public_url: 'https://discord-bot.example.com',
        status: 'online',
        health_status: 'healthy',
        current_stream_id: streamID,
        last_heartbeat_at: now,
        heartbeat_age_sec: 3,
        assignment_role: 'primary',
        capabilities: { audio_capture: true, audio_stream_forward: true },
        metrics: {
          'discord.voice_connected': 1,
          'discord.gateway_connected': 1,
          'discord.audio_receiving': 1,
          'discord.audio_forward_enabled': 1,
          'discord.audio_forward_active': 0,
          'discord.audio_packets_total': 180,
          'discord.audio_forwarded_total': 120,
          'discord.audio_forward_errors_total': 4,
          'discord.audio_last_packet_age_sec': 1,
          'discord.audio_last_forward_age_sec': 12,
          'discord.reconnect_count': 3,
          'discord.voice_disconnect_count': 1,
          'discord.participant_count': 3,
        },
      },
      {
        service_id: 'encoder-demo',
        service_type: 'encoder_recorder',
        service_name: 'Encoder Recorder Demo',
        public_url: 'https://encoder.example.com',
        status: 'online',
        health_status: 'healthy',
        current_stream_id: streamID,
        last_heartbeat_at: now,
        heartbeat_age_sec: 4,
        assignment_role: 'primary',
        capabilities: { rtmps_output: true, archive_upload: true, discord_audio_ingest: true },
        metrics: {
          'encoder.process_alive': 1,
          'encoder.output_fps': 60,
          'encoder.output_bitrate_kbps': 7800,
          'media.input_timeout_sec': 0,
        },
      },
      {
        service_id: 'worker-demo',
        service_type: 'worker',
        service_name: 'Worker Demo',
        public_url: 'https://worker.example.com',
        status: 'online',
        health_status: 'healthy',
        current_stream_id: streamID,
        last_heartbeat_at: now,
        heartbeat_age_sec: 5,
        assignment_role: 'primary',
        capabilities: { overlay_events: true, caption_events: true, participant_state: true },
        metrics: {
          'worker.overlay_events_total': 32,
          'worker.caption_events_total': 8,
          'worker.event_send_failures_total': 0,
        },
      },
      {
        service_id: 'encoder-standby-demo',
        service_type: 'encoder_recorder',
        service_name: 'Encoder Standby Demo',
        public_url: 'https://encoder-standby.example.com',
        status: 'online',
        health_status: 'healthy',
        current_stream_id: '',
        last_heartbeat_at: now,
        heartbeat_age_sec: 8,
        assignment_role: 'standby',
        capabilities: { rtmps_output: true, archive_upload: true },
        metrics: { 'encoder.process_alive': 0 },
      },
    ],
    '/workers': [
      { id: 'worker-demo', service_id: 'worker-demo', service_type: 'worker', service_name: 'Worker Demo', status: 'online', health_status: 'healthy', assignment_role: 'primary', current_stream_id: streamID },
    ],
    '/profiles/encoder': [
      { id: 'encoder-profile-demo', name: '1080p60 default', config: { width: 1920, height: 1080, fps: 60 } },
    ],
    '/profiles/archive': [
      {
        id: 'archive-profile-demo',
        name: 'OAuth shared drive archive',
        config: {
          upload_enabled: true,
          drive_destination_id: 'drive-destination-demo',
          auth_mode: 'oauth2',
        },
      },
    ],
    '/discord/configs': [
      {
        id: 'discord-config-demo',
        name: 'Demo VC',
        service_id: 'discord-bot-demo',
        guild_id: '<DISCORD_GUILD_ID>',
        voice_channel_id: '<VOICE_CHANNEL_ID>',
        text_channel_id: '<TEXT_CHANNEL_ID>',
        bot_token_configured: true,
        bot_token_fingerprint: 'demo-fp',
        audio_forward_enabled: true,
        reconnect_enabled: true,
        reconnect_max_attempts: 5,
        reconnect_base_delay: '2s',
        reconnect_max_delay: '30s',
      },
    ],
    '/youtube/outputs': [
      {
        id: 'youtube-output-demo',
        name: 'Demo YouTube Live API output',
        mode: 'live_api_dry_run',
        rtmp_url: 'rtmps://example.youtube.com/live2',
        oauth_account_id: 'oauth-account-demo',
        stream_key_configured: false,
        enable_auto_start: true,
        enable_auto_stop: true,
        privacy_status: 'private',
        latency_preference: 'low',
      },
    ],
    '/profiles/caption': [],
    '/profiles/overlay': [],
    '/integrations/oauth-providers': [
      {
        id: 'oauth-provider-demo',
        provider_type: 'google',
        name: 'Google Drive / YouTube',
        enabled: true,
        client_id: 'google-client-id.apps.exampleusercontent.com',
        client_secret_configured: true,
        scopes: ['https://www.googleapis.com/auth/drive.file', 'https://www.googleapis.com/auth/youtube'],
        redirect_uri: 'https://control.example.com/integrations/oauth-accounts/callback',
      },
    ],
    '/integrations/oauth-accounts': [
      {
        id: 'oauth-account-demo',
        provider_id: 'oauth-provider-demo',
        provider_type: 'google',
        account_label: 'Archive / YouTube owner',
        email: 'owner@example.com',
        refresh_token_configured: true,
        scopes: ['https://www.googleapis.com/auth/drive.file', 'https://www.googleapis.com/auth/youtube'],
        created_at: now,
        updated_at: now,
      },
    ],
    '/archive/destinations': [
      {
        id: 'drive-destination-demo',
        name: 'Shared Drive Archive Folder',
        auth_mode: 'oauth2',
        oauth_account_id: 'oauth-account-demo',
        folder_id_configured: true,
        folder_id_fingerprint: 'drive-folder-demo-fp',
        shared_drive: true,
        base_path: 'AutoStream',
        created_at: now,
        updated_at: now,
      },
    ],
    [`/streams/${streamID}/external-e2e-config`]: {
      schema_version: 1,
      stream_id: streamID,
      runtime_config: {
        youtube_output_id: 'youtube-output-demo',
        drive_destination_id: 'drive-destination-demo',
        discord_config_id: 'discord-config-demo',
        encoder_profile_id: 'encoder-profile-demo',
        archive_profile_id: 'archive-profile-demo',
      },
      service_assignments: {
        discord_bot_service_id: 'discord-bot-demo',
        encoder_recorder_primary_service_id: 'encoder-demo',
        worker_primary_service_id: 'worker-demo',
        encoder_recorder_standby_service_id: 'encoder-standby-demo',
        worker_standby_service_id: '',
      },
      confirmations: {
        youtube_output_saved: true,
        drive_destination_saved: true,
        discord_config_saved: true,
        primary_assignments_saved: true,
        runtime_config_distribution_enabled: true,
      },
      readiness: {
        ready: true,
        missing_confirmations: [],
        missing_runtime_ids: [],
        missing_primary_services: [],
        missing_runtime_config_capabilities: [],
      },
    },
    [`/streams/${incompleteStreamID}/external-e2e-config`]: {
      schema_version: 1,
      stream_id: incompleteStreamID,
      runtime_config: {
        youtube_output_id: '',
        drive_destination_id: '',
        discord_config_id: '',
        encoder_profile_id: '',
        archive_profile_id: '',
      },
      service_assignments: {
        discord_bot_service_id: '',
        encoder_recorder_primary_service_id: '',
        worker_primary_service_id: '',
        encoder_recorder_standby_service_id: '',
        worker_standby_service_id: '',
      },
      confirmations: {
        youtube_output_saved: false,
        drive_destination_saved: false,
        discord_config_saved: false,
        primary_assignments_saved: false,
        runtime_config_distribution_enabled: false,
      },
      readiness: {
        ready: false,
        missing_confirmations: ['youtube_output_saved', 'drive_destination_saved', 'discord_config_saved', 'primary_assignments_saved', 'runtime_config_distribution_enabled'],
        missing_runtime_ids: ['youtube_output_id', 'drive_destination_id', 'discord_config_id', 'encoder_profile_id', 'archive_profile_id'],
        missing_primary_services: ['discord_bot', 'encoder_recorder', 'worker'],
        missing_runtime_config_capabilities: [],
      },
    },
    '/service-health/discord-bot-demo/runtime-config': {
      service: {
        service_id: 'discord-bot-demo',
        service_type: 'discord_bot',
        service_name: 'Discord Bot Demo',
        public_url: 'https://discord-bot.example.com',
        version: '0.1.0',
        status: 'online',
        current_stream_id: streamID,
        assignment_role: 'primary',
        capabilities: { audio_capture: true, audio_stream_forward: true, runtime_config: true },
      },
      assignments: [
        { service_id: 'discord-bot-demo', service_type: 'discord_bot', stream_id: streamID, assignment_role: 'primary' },
      ],
      profiles: {
        discord_config: [
          {
            id: 'discord-config-demo',
            kind: 'discord_config',
            name: 'Demo VC',
            config: {
              service_id: 'discord-bot-demo',
              guild_id: '<DISCORD_GUILD_ID>',
              voice_channel_id: '<VOICE_CHANNEL_ID>',
              bot_token_secret_name: 'discord_bot_token_demo',
            },
          },
        ],
      },
      stream_discord_configs: [
        {
          stream_id: streamID,
          assignment_role: 'primary',
          discord_config_id: 'discord-config-demo',
          guild_id: '<DISCORD_GUILD_ID>',
          voice_channel_id: '<VOICE_CHANNEL_ID>',
          text_channel_id: '<TEXT_CHANNEL_ID>',
        },
      ],
    },
    '/service-health/encoder-demo/runtime-config': {
      service: {
        service_id: 'encoder-demo',
        service_type: 'encoder_recorder',
        service_name: 'Encoder Recorder Demo',
        public_url: 'https://encoder.example.com',
        version: '0.1.0',
        status: 'online',
        current_stream_id: streamID,
        assignment_role: 'primary',
        capabilities: { rtmps_output: true, archive_upload: true, discord_audio_ingest: true, runtime_config: true },
      },
      assignments: [
        { service_id: 'encoder-demo', service_type: 'encoder_recorder', stream_id: streamID, assignment_role: 'primary' },
      ],
      profiles: {
        archive: [
          {
            id: 'archive-profile-demo',
            kind: 'archive',
            name: 'OAuth shared drive archive',
            config: {
              drive_destination_id: 'drive-destination-demo',
              auth_mode: 'oauth2',
              folder_id_secret_name: 'google_drive_folder_id_demo',
              refresh_token_secret_name: 'google_oauth_refresh_token_demo',
              shared_drive: true,
            },
          },
        ],
        youtube_output: [
          {
            id: 'youtube-output-demo',
            kind: 'youtube_output',
            name: 'Demo YouTube Live API output',
            config: {
              mode: 'live_api_dry_run',
              output_id: 'youtube-output-demo',
              rtmp_url: 'rtmps://example.youtube.com/live2',
              stream_key_secret_name: 'youtube_stream_key_runtime_demo',
            },
          },
        ],
      },
      stream_archive_configs: [
        {
          stream_id: streamID,
          assignment_role: 'primary',
          archive_profile_id: 'archive-profile-demo',
          ready: true,
          archive_config: {
            drive_destination_id: 'drive-destination-demo',
            auth_mode: 'oauth2',
            folder_id_secret_name: 'google_drive_folder_id_demo',
            refresh_token_secret_name: 'google_oauth_refresh_token_demo',
            shared_drive: true,
          },
        },
      ],
      stream_youtube_configs: [
        {
          stream_id: streamID,
          assignment_role: 'primary',
          youtube_output_id: 'youtube-output-demo',
          ready: true,
          youtube_config: {
            mode: 'live_api_dry_run',
            output_id: 'youtube-output-demo',
            rtmp_url: 'rtmps://example.youtube.com/live2',
            stream_key_secret_name: 'youtube_stream_key_runtime_demo',
          },
        },
      ],
    },
    '/audit-logs': [
      { id: 'audit-demo', timestamp: now, action: 'streams.start', actor_username: 'demo-admin', result: 'success', resource_type: 'stream', resource_id: streamID },
    ],
    '/observability/incidents': [
      {
        id: incidentID,
        rule: 'discord_audio_forward_stale',
        severity: 'warning',
        status: 'open',
        summary_ja: discordForwardReport.summary,
        service_id: 'discord-bot-demo',
        stream_id: streamID,
        signal_id: 'signal-discord-forward-age',
        diagnostic_report: discordForwardReport,
        opened_at: now,
        updated_at: now,
      },
      {
        id: voiceDisconnectIncidentID,
        rule: 'discord_voice_disconnected',
        severity: 'error',
        status: 'open',
        summary_ja: discordVoiceDisconnectReport.summary,
        service_id: 'discord-bot-demo',
        stream_id: streamID,
        signal_id: 'signal-discord-voice-disconnect',
        diagnostic_report: discordVoiceDisconnectReport,
        opened_at: now,
        updated_at: now,
      },
    ],
    '/observability/diagnostics': [
      {
        id: incidentID,
        incident_id: incidentID,
        rule: 'discord_audio_forward_stale',
        service_id: 'discord-bot-demo',
        stream_id: streamID,
        diagnostic_report: discordForwardReport,
        updated_at: now,
      },
      {
        id: voiceDisconnectIncidentID,
        incident_id: voiceDisconnectIncidentID,
        rule: 'discord_voice_disconnected',
        service_id: 'discord-bot-demo',
        stream_id: streamID,
        diagnostic_report: discordVoiceDisconnectReport,
        updated_at: now,
      },
    ],
    '/observability/remediation-actions': [
      { id: 'remediation-demo', incident_id: incidentID, action: 'restart_discord_bot', mode: 'manual_approval', status: 'pending_approval', safe_auto: false, requires_approval: true, created_at: now, updated_at: now },
      { id: 'remediation-voice-reconnect-demo', incident_id: voiceDisconnectIncidentID, action: 'reconnect_discord_voice', mode: 'manual_approval', status: 'pending_approval', safe_auto: false, requires_approval: true, created_at: now, updated_at: now },
    ],
    '/observability/metrics': [
      metric('discord.audio_forward_errors_total', 4, 'discord-bot-demo', 'discord_bot'),
      metric('discord.audio_forwarded_total', 120, 'discord-bot-demo', 'discord_bot'),
      metric('discord.audio_last_forward_age_sec', 12, 'discord-bot-demo', 'discord_bot'),
      metric('discord.audio_receiving', 1, 'discord-bot-demo', 'discord_bot'),
      metric('discord.reconnect_count', 3, 'discord-bot-demo', 'discord_bot'),
      metric('discord.voice_disconnect_count', 1, 'discord-bot-demo', 'discord_bot'),
      metric('media.input_timeout_sec', 0, 'encoder-demo', 'encoder_recorder'),
    ],
    '/observability/notification-deliveries': [
      {
        id: 'delivery-demo-email',
        event_type: 'incident.opened',
        channel_type: 'email',
        channel_id: 'notify-email-demo',
        target: 'o***s@example.com',
        status: 'delivered',
        attempts: 1,
        created_at: now,
      },
    ],
    '/observability/notification-channels': [
      {
        id: 'notify-email-demo',
        name: 'Ops email',
        type: 'email',
        enabled: true,
        severity_filter: ['warning', 'error', 'critical'],
        event_type_filter: ['incident.opened', 'stream.failed'],
        smtp_password_configured: true,
        masked_email_target: 'o***s@example.com',
      },
    ],
    [`/streams/${streamID}/encoder-preflight`]: {
      preflight: { ready: true, ffmpeg_available: true, archive_dir_writable: true, rtmp_configured: true },
    },
    [`/streams/${streamID}/audio-status`]: {
      audio_bridge_status: {
        stream_id: streamID,
        bridge_active: true,
        packets_total: 180,
        rtp_forwarded: 120,
        last_packet_age_sec: 1,
        last_packet_at: now,
      },
    },
    [`/streams/${streamID}/worker-events`]: {
      events: [
        { event_type: 'current_time', created_at: now },
        { event_type: 'participant_state', created_at: now },
      ],
    },
  };
  if (Object.prototype.hasOwnProperty.call(byPath, normalizedPath)) {
    return byPath[normalizedPath];
  }
  return shape === 'object' ? {} : [];
}

function metric(name, value, serviceID, serviceType) {
  return { name, value, service_id: serviceID, service_type: serviceType, stream_id: streamID, updated_at: now };
}

function stripQuery(path) {
  return String(path || '').split('?')[0];
}
