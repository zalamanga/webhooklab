export type State =
  | "QUEUED"
  | "SENDING"
  | "RETRY_WAIT"
  | "DELIVERED"
  | "DEAD_LETTER";
export type AttemptStatus =
  | "STARTED"
  | "SUCCEEDED"
  | "HTTP_ERROR"
  | "TIMEOUT"
  | "NETWORK_ERROR"
  | "ABANDONED";
export type DestinationId = "warehouse" | "billing";
export type ReceiverMode = "HEALTHY" | "FAIL" | "TIMEOUT";
export type EventType = "order.created" | "order.cancelled";
export interface User {
  id: string;
  email: string;
  name: string;
}
export interface Session {
  user: User;
  csrfToken: string;
}
export interface QueueControl {
  paused: boolean;
  version: number;
}
export interface WorkerStatus {
  activeCount: number;
  lastSeenAt: string | null;
  staleAfterSeconds: 15;
}
export interface Dashboard {
  total: number;
  counts: Record<State, number>;
  queue: QueueControl;
  workers: WorkerStatus;
  serverTime: string;
}
export interface Destination {
  id: DestinationId;
  name: string;
  route: string;
  mode: ReceiverMode;
  version: number;
}
export interface Delivery {
  id: string;
  eventType: string;
  destinationId: DestinationId;
  destinationName: string;
  payload: string;
  state: State;
  version: number;
  cycle: number;
  cycleAttempts: number;
  totalAttempts: number;
  redrives: number;
  nextAttemptAt: string | null;
  createdAt: string;
  updatedAt: string;
}
export interface Attempt {
  id: string;
  number: number;
  cycle: number;
  status: AttemptStatus;
  startedAt: string;
  finishedAt: string | null;
  httpStatus: number | null;
  durationMs: number | null;
  responsePreview: string | null;
  errorCategory: string | null;
  workerId: string;
}
export interface Receipt {
  eventId: string;
  destinationId: DestinationId;
  firstReceivedAt: string;
  lastReceivedAt: string;
  receiptCount: number;
  effectCount: 1;
  bodySha256: string;
}
export interface AuditItem {
  id: string;
  createdAt: string;
  actor: string;
  action: string;
  eventId: string | null;
  note: string;
}
export interface Page<T> {
  items: T[];
  page: number;
  pageSize: 25;
  total: number;
}
export interface DeliveryDetail {
  delivery: Delivery;
  attempts: Attempt[];
  receipt: Receipt | null;
  audit: AuditItem[];
}
