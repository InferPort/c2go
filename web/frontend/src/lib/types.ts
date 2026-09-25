export interface CloudflareZone {
  name: string;
}

export interface DNSRecord {
  id: string;
  name: string;
  type: string;
  content: string;
  proxied: boolean;
}

export interface ManagedRecord {
  id: string;
  name: string;
  type: string;
  proxied: boolean;
}

export interface ManagedZone {
  domain: string;
  records: ManagedRecord[];
}

export interface NetworkInterface {
  name: string;
  ips: string[];
  flags: string[];
  up: boolean;
  mac: string;
}

export interface ServiceStatusInfo {
  type: 'systemd' | 'launchd' | 'windows' | 'standalone';
  is_installed: boolean;
  is_running: boolean;
  pid?: number;
  status_label: string;
  suggested_cmd: string;
  action_note: string;
}

export interface ConfigPayload {
  token: string;
  language: string;
  update_interval: number;
  preferred_interfaces: string[];
  history_enabled: boolean;
  check_updates: boolean;
  auto_update: boolean;
  managed_zones: ManagedZone[];
}
