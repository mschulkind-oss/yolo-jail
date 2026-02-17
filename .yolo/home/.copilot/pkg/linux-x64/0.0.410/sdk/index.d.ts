import type { ChatCompletionAssistantMessageParam } from 'openai/resources/chat/completions';
import { ChatCompletionChunk } from 'openai/resources';
import type { ChatCompletionMessage } from 'openai/resources/chat/completions';
import type { ChatCompletionMessageParam } from 'openai/resources/chat/completions';
import { ChatCompletionMessageToolCall } from 'openai/resources';
import type { ChatCompletionTool } from 'openai/resources/chat/completions';
import type { ChatCompletionToolChoiceOption } from 'openai/resources/chat/completions';
import { ChatCompletionToolMessageParam } from 'openai/resources/chat/completions';
import { ChatCompletionUserMessageParam } from 'openai/resources/chat/completions';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { JSONRPCMessage } from '@modelcontextprotocol/sdk/types.js';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { OAuthClientProvider } from '@modelcontextprotocol/sdk/client/auth.js';
import { OpenAI } from 'openai/index.mjs';
import type { ReasoningEffort } from 'openai/resources';
import { SSEClientTransport } from '@modelcontextprotocol/sdk/client/sse.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import { Transport } from '@modelcontextprotocol/sdk/shared/transport.js';
import { Writable } from 'stream';
import z from 'zod';
import * as z_2 from 'zod';
import { ZodObject } from 'zod';
import { ZodOptional } from 'zod';
import { ZodString } from 'zod';
import { ZodTypeAny } from 'zod';

export declare type AbortEvent = z_2.infer<typeof AbortEventSchema>;

/**
 * Turn abort event - User aborted the current turn
 * Triggers completion of orphaned tool calls
 */
declare const AbortEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"abort">;
    data: z_2.ZodObject<{
        reason: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        reason: string;
    }, {
        reason: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        reason: string;
    };
    id: string;
    type: "abort";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        reason: string;
    };
    id: string;
    type: "abort";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare type AgentAction = (typeof AgentActions)[number];

declare const AgentActions: readonly ["fix", "fix-pr-comment", "task"];

declare type AgentIdBag = {
    /**
     * The ID of the agent whose invocation generated the object this bag is attached to.
     */
    agentId?: string;
};

export declare type AgentMode = z_2.infer<typeof AgentModeSchema>;

/** The UI mode the agent is operating in */
declare const AgentModeSchema: z_2.ZodEnum<["interactive", "plan", "autopilot", "shell"]>;

export declare type AgentStopHook = (input: AgentStopHookInput) => Promise<AgentStopHookOutput | void>;

/**
 * Agent stop hook types - fires when the agent naturally stops (no more tool calls)
 */
export declare interface AgentStopHookInput extends BaseHookInput {
    sessionId: string;
    transcriptPath: string;
    /**
     * The reason the agent stopped. Currently only "end_turn" is supported.
     * Matches Claude Code terminology; extensible for future stop reasons.
     */
    stopReason: "end_turn";
}

export declare interface AgentStopHookOutput {
    /** If "block", the agent will continue with another turn using the reason. Undefined means "allow". */
    decision?: "block" | "allow";
    reason?: string;
}

/**
 * A background agent task (subagent running in background mode).
 */
export declare type AgentTask = {
    type: "agent";
    id: string;
    /** Tool call ID for correlating session events with this agent */
    toolCallId: string;
    description: string;
    status: BackgroundTaskStatus;
    startedAt: number;
    completedAt?: number;
    error?: string;
    agentType: string;
    prompt: string;
    result?: string;
    modelOverride?: string;
};

/**
 * Progress for a background agent task, derived from session events.
 */
export declare type AgentTaskProgress = {
    type: "agent";
    /** Recent tool execution events converted to display lines */
    recentActivity: ProgressLine[];
    /** The most recent intent reported by the agent */
    latestIntent?: string;
};

declare type ApiKeyAuthInfo = {
    readonly type: "api-key";
    readonly apiKey: string;
    readonly host: string;
    readonly copilotUser?: CopilotUserResponse;
};

declare type ApiMethodSchema = {
    params?: z.ZodTypeAny;
    result?: z.ZodTypeAny;
    stability?: ApiStability;
};

/** Constrains a schema to only contain methods or nested namespaces of methods. */
declare type ApiSchema = {
    [key: string]: ApiMethodSchema | ApiSchema;
};

/** Derive the required implementation shape from an API schema.
 *
 * Each leaf entry becomes a function matching its params/result.
 * Nested objects become nested namespaces.
 */
declare type ApiSchemaToInterface<S extends ApiSchema> = {
    [K in keyof S]: S[K] extends ApiMethodSchema ? InferMethod<S[K]> : S[K] extends ApiSchema ? ApiSchemaToInterface<S[K]> : never;
};

declare type ApiStability = "experimental" | "stable";

/**
 * An assessed command, with its identifier and whether it is read-only.
 */
declare type AssessedCommand = {
    /**
     * The command identifier, e.g. "rm" or "git push".
     */
    readonly identifier: string;
    /**
     * Whether the command is read-only (i.e. does not modify state).
     */
    readOnly: boolean;
};

export declare type AssistantIntentEvent = z_2.infer<typeof AssistantIntentEventSchema>;

declare const AssistantIntentEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.intent">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        intent: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        intent: string;
    }, {
        intent: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        intent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.intent";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        intent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.intent";
    timestamp: string;
    parentId: string | null;
}>;

export declare type AssistantMessageDeltaEvent = z_2.infer<typeof AssistantMessageDeltaEventSchema>;

/**
 * Assistant message streaming delta (ephemeral, not persisted)
 * Sent when streaming is enabled to provide incremental response chunks.
 * Clients should accumulate deltaContent to build the full response.
 */
declare const AssistantMessageDeltaEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.message_delta">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        messageId: z_2.ZodString;
        deltaContent: z_2.ZodString;
        totalResponseSizeBytes: z_2.ZodOptional<z_2.ZodNumber>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    }, {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.message_delta";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.message_delta";
    timestamp: string;
    parentId: string | null;
}>;

export declare type AssistantMessageEvent = z_2.infer<typeof AssistantMessageEventSchema>;

/**
 * An event that is emitted by the `Client` for each message it receives from the LLM.
 *
 * Currently does not include telemetry.
 */
declare type AssistantMessageEvent_2 = {
    kind: "message";
    turn?: number;
    callId?: string;
    modelCall?: ModelCallParam;
    message: ChatCompletionMessageParamsWithToolCalls & ReasoningMessageParam;
};

/**
 * Assistant text response (with optional tool requests)
 */
declare const AssistantMessageEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.message">;
    data: z_2.ZodObject<{
        messageId: z_2.ZodString;
        content: z_2.ZodString;
        toolRequests: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
            toolCallId: z_2.ZodString;
            name: z_2.ZodString;
            arguments: z_2.ZodUnknown;
            type: z_2.ZodOptional<z_2.ZodEnum<["function", "custom"]>>;
        }, "strip", z_2.ZodTypeAny, {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }, {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }>, "many">>;
        reasoningOpaque: z_2.ZodOptional<z_2.ZodString>;
        reasoningText: z_2.ZodOptional<z_2.ZodString>;
        encryptedContent: z_2.ZodOptional<z_2.ZodString>;
        /** Phase of generation for phased-output models (e.g. gpt-5.3-codex). */
        phase: z_2.ZodOptional<z_2.ZodString>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    }, {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "assistant.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "assistant.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type AssistantReasoningDeltaEvent = z_2.infer<typeof AssistantReasoningDeltaEventSchema>;

/**
 * Assistant reasoning streaming delta (ephemeral, not persisted)
 * Sent when streaming is enabled to provide incremental reasoning chunks.
 * Clients should accumulate deltaContent to build the full reasoning.
 */
declare const AssistantReasoningDeltaEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.reasoning_delta">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        reasoningId: z_2.ZodString;
        deltaContent: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        reasoningId: string;
        deltaContent: string;
    }, {
        reasoningId: string;
        deltaContent: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        reasoningId: string;
        deltaContent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.reasoning_delta";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        reasoningId: string;
        deltaContent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.reasoning_delta";
    timestamp: string;
    parentId: string | null;
}>;

export declare type AssistantReasoningEvent = z_2.infer<typeof AssistantReasoningEventSchema>;

/**
 * Assistant reasoning content (chain-of-thought), ephemeral event for UI timeline
 * The reasoning text is persisted in assistant.message.reasoningText field instead
 */
declare const AssistantReasoningEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.reasoning">;
    data: z_2.ZodObject<{
        reasoningId: z_2.ZodString;
        content: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        reasoningId: string;
    }, {
        content: string;
        reasoningId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        reasoningId: string;
    };
    id: string;
    type: "assistant.reasoning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        reasoningId: string;
    };
    id: string;
    type: "assistant.reasoning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type AssistantTurnEndEvent = z_2.infer<typeof AssistantTurnEndEventSchema>;

/**
 * Agent completes a turn
 */
declare const AssistantTurnEndEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.turn_end">;
    data: z_2.ZodObject<{
        turnId: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        turnId: string;
    }, {
        turnId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type AssistantTurnStartEvent = z_2.infer<typeof AssistantTurnStartEventSchema>;

/**
 * Agent starts processing a turn
 */
declare const AssistantTurnStartEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.turn_start">;
    data: z_2.ZodObject<{
        turnId: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        turnId: string;
    }, {
        turnId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type AssistantUsageEvent = z_2.infer<typeof AssistantUsageEventSchema>;

/**
 * Assistant usage metrics (ephemeral event for UI updates)
 * Used to track model usage, tokens, costs, and durations without persisting to disk
 * Emitted in app.tsx's onModelCallSuccess callback
 */
declare const AssistantUsageEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.usage">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        model: z_2.ZodString;
        inputTokens: z_2.ZodOptional<z_2.ZodNumber>;
        outputTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cacheReadTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cacheWriteTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cost: z_2.ZodOptional<z_2.ZodNumber>;
        duration: z_2.ZodOptional<z_2.ZodNumber>;
        initiator: z_2.ZodOptional<z_2.ZodString>;
        apiCallId: z_2.ZodOptional<z_2.ZodString>;
        providerCallId: z_2.ZodOptional<z_2.ZodString>;
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
        quotaSnapshots: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodObject<{
            isUnlimitedEntitlement: z_2.ZodBoolean;
            entitlementRequests: z_2.ZodNumber;
            usedRequests: z_2.ZodNumber;
            usageAllowedWithExhaustedQuota: z_2.ZodBoolean;
            overage: z_2.ZodNumber;
            overageAllowedWithExhaustedQuota: z_2.ZodBoolean;
            remainingPercentage: z_2.ZodNumber;
            resetDate: z_2.ZodOptional<z_2.ZodDate>;
        }, "strip", z_2.ZodTypeAny, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }>>>;
    }, "strip", z_2.ZodTypeAny, {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    }, {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.usage";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.usage";
    timestamp: string;
    parentId: string | null;
}>;

export declare type Attachment = z_2.infer<typeof AttachmentSchema>;

declare const AttachmentSchema: z_2.ZodDiscriminatedUnion<"type", [z_2.ZodObject<{
    path: z_2.ZodString;
    displayName: z_2.ZodString;
    lineRange: z_2.ZodOptional<z_2.ZodObject<{
        start: z_2.ZodNumber;
        end: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        end: number;
        start: number;
    }, {
        end: number;
        start: number;
    }>>;
} & {
    type: z_2.ZodLiteral<"file">;
}, "strip", z_2.ZodTypeAny, {
    type: "file";
    path: string;
    displayName: string;
    lineRange?: {
        end: number;
        start: number;
    } | undefined;
}, {
    type: "file";
    path: string;
    displayName: string;
    lineRange?: {
        end: number;
        start: number;
    } | undefined;
}>, z_2.ZodObject<{
    path: z_2.ZodString;
    displayName: z_2.ZodString;
    lineRange: z_2.ZodOptional<z_2.ZodObject<{
        start: z_2.ZodNumber;
        end: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        end: number;
        start: number;
    }, {
        end: number;
        start: number;
    }>>;
} & {
    type: z_2.ZodLiteral<"directory">;
}, "strip", z_2.ZodTypeAny, {
    type: "directory";
    path: string;
    displayName: string;
    lineRange?: {
        end: number;
        start: number;
    } | undefined;
}, {
    type: "directory";
    path: string;
    displayName: string;
    lineRange?: {
        end: number;
        start: number;
    } | undefined;
}>, z_2.ZodObject<{
    type: z_2.ZodLiteral<"selection">;
    filePath: z_2.ZodString;
    displayName: z_2.ZodString;
    text: z_2.ZodString;
    selection: z_2.ZodObject<{
        start: z_2.ZodObject<{
            line: z_2.ZodNumber;
            character: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            line: number;
            character: number;
        }, {
            line: number;
            character: number;
        }>;
        end: z_2.ZodObject<{
            line: z_2.ZodNumber;
            character: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            line: number;
            character: number;
        }, {
            line: number;
            character: number;
        }>;
    }, "strip", z_2.ZodTypeAny, {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    }, {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    }>;
}, "strip", z_2.ZodTypeAny, {
    type: "selection";
    text: string;
    filePath: string;
    displayName: string;
    selection: {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    };
}, {
    type: "selection";
    text: string;
    filePath: string;
    displayName: string;
    selection: {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    };
}>]>;

export declare type AudioContent = z_2.infer<typeof AudioContentSchema>;

/**
 * Audio content block with base64-encoded data
 */
declare const AudioContentSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"audio">;
    data: z_2.ZodString;
    mimeType: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    data: string;
    type: "audio";
    mimeType: string;
}, {
    data: string;
    type: "audio";
    mimeType: string;
}>;

declare type AuthCallback = (authInfo: AuthInfo | null, token?: string) => void | Promise<void>;

/**
 * Represents the authentication information for a user.
 */
declare type AuthInfo = HMACAuthInfo | EnvAuthInfo | UserAuthInfo | GhCliAuthInfo | ApiKeyAuthInfo | TokenAuthInfo | CopilotApiTokenAuthInfo;

/**
 * Authentication information along with an optional token.
 */
declare type AuthInfoWithToken = {
    authInfo: AuthInfo;
    token?: string;
};

declare class AuthManager {
    private readonly featureFlags;
    private currentAuthInfo;
    private loadingAuthInfo?;
    private authCallbacks;
    private config;
    constructor(featureFlags?: FeatureFlags, config?: AuthManagerConfig);
    /**
     * Register a callback to be called when authentication state changes
     * If there is existing auth info, the callback is called immediately
     * @param callback Function to call with auth info and token when auth state changes
     */
    onAuthChange(callback: AuthCallback): void;
    /**
     * Remove an authentication change callback
     * @param callback The callback to remove
     */
    removeAuthCallback(callback: AuthCallback): void;
    /**
     * Notify all registered callbacks of auth state change
     * @param authInfo Current auth info (null for logout)
     * @param token Optional token
     */
    private notifyAuthChange;
    /**
     * Attempts to use SDK-provided token from specified environment variable.
     * This is the highest priority auth method when authTokenEnvVar is configured.
     * @returns Auth info + token if the env var is set and contains a valid token.
     */
    private trySdkTokenLogin;
    /**
     * Attempts to use HMAC authentication.
     * @returns Auth info if authentication was successful, undefined otherwise.
     */
    private tryHMACLogin;
    /**
     * Attempts to use GITHUB_ASKPASS to retrieve the GitHub token.
     * @returns Token if available from GITHUB_ASKPASS, undefined otherwise.
     */
    private getGitHubAskpassToken;
    /**
     * Attempts to use COPILOT_GITHUB_TOKEN/GH_TOKEN/GITHUB_TOKEN env vars for authentication.
     * @returns Auth info + token if authentication was successful, undefined otherwise.
     */
    private tryGitHubTokenLogin;
    /** Attempts to use GitHub CLI for authentication. */
    private tryGhCliTokenLogin;
    /** Attempts to use GITHUB_COPILOT_API_TOKEN for authentication when COPILOT_API_URL is also set. */
    private tryCopilotApiTokenLogin;
    private tryApiKeyLogin;
    /** Attempts to use CLI OAuth token for authentication. */
    private tryLoginToken;
    /** Attempts to use CLI OAuth token for authentication. */
    private getAllGitHubLoginTokens;
    /**
     * Returns all the auth options available right now, sorted by priority.
     * @returns List of all available auth options, along with the token if available.
     */
    getAllAuthAvailable(): Promise<AuthInfoWithToken[]>;
    private getPrioritizedAuthMethods;
    private loadAuthInfo;
    /**
     * Returns the current auth info. Loads it lazily if not available.
     * @returns Current auth info.
     */
    getCurrentAuthInfo(): Promise<AuthInfo | null>;
    /**
     * Updates the current auth info with the provided user information.
     * @param host User's host
     * @param login User's login
     */
    loginUser(host: string, login: string, token: string): Promise<AuthInfo>;
    /**
     * Switches to the specified authentication info.
     * @param auth The authentication method to switch to.
     */
    switchToAuth(auth: AuthInfoWithToken): Promise<void>;
    /**
     * Clears the current credentials.
     * @returns True if, after logging out a user, there are more users logged in.
     *          False otherwise.
     */
    logout(): Promise<boolean>;
}

/**
 * Configuration options for AuthManager.
 */
declare interface AuthManagerConfig {
    /**
     * If set, read auth token from this specific environment variable.
     * This takes highest priority and bypasses normal auth method precedence.
     */
    authTokenEnvVar?: string;
    /**
     * If true, disable automatic login detection (stored OAuth tokens and gh CLI).
     */
    disableAutoLogin?: boolean;
}

/**
 * Represents a background agent execution.
 */
declare interface BackgroundAgent {
    /** Unique identifier for this agent execution */
    agentId: string;
    /** Tool call ID used for correlating session events with this agent */
    toolCallId: string;
    /** Type of agent being run */
    agentType: string;
    /** Short description of the task */
    description: string;
    /** The prompt sent to the agent */
    prompt: string;
    /** Current status of the agent */
    status: BackgroundAgentStatus;
    /** When the agent was started */
    startedAt: number;
    /** When the agent completed (if completed) */
    completedAt?: number;
    /** The result if completed */
    result?: ToolResultExpanded;
    /** Error message if failed */
    error?: string;
    /** Model override if specified */
    modelOverride?: string;
}

/**
 * Registry for tracking background agent executions.
 * Similar to InteractiveShellToolContext but for agent executions.
 */
declare class BackgroundAgentRegistry {
    private readonly agents;
    private readonly pendingPromises;
    private readonly abortControllers;
    private onChangeCallback?;
    /**
     * Set a callback to be notified when agents are started or complete.
     * @param callback - Function to call when agents change
     */
    setOnChangeCallback(callback: () => void): void;
    /**
     * Starts a background agent execution.
     * @param agentType - The type of agent to run
     * @param description - Short description of the task
     * @param prompt - The prompt to send to the agent
     * @param executeAgent - Function that actually executes the agent, receives an AbortSignal
     * @param modelOverride - Optional model override
     * @param toolCallId - The tool call ID from the parent, used to correlate session events
     * @returns The agent ID for tracking
     */
    start(agentType: string, description: string, prompt: string, executeAgent: (abortSignal: AbortSignal) => Promise<ToolResultExpanded>, modelOverride?: string, toolCallId?: string): string;
    /**
     * Gets the status of a background agent.
     * @param agentId - The agent ID to check
     * @returns The agent info, or undefined if not found
     */
    getStatus(agentId: string): BackgroundAgent | undefined;
    /**
     * Gets the result of a completed background agent.
     * If the agent is still running, optionally waits for completion.
     * @param agentId - The agent ID to get results for
     * @param wait - Whether to wait for completion if still running
     * @param timeoutMs - Maximum time to wait in milliseconds (default: 30000)
     * @returns The result, or undefined if not found or timed out
     */
    getResult(agentId: string, wait?: boolean, timeoutMs?: number): Promise<{
        agent: BackgroundAgent;
        result?: ToolResultExpanded;
        timedOut?: boolean;
    } | undefined>;
    /**
     * Lists all background agents.
     * @param includeCompleted - Whether to include completed agents (default: true)
     * @returns Array of all tracked agents
     */
    list(includeCompleted?: boolean): BackgroundAgent[];
    /**
     * Clears completed agents from the registry.
     * Useful for cleanup after a session.
     */
    clearCompleted(): void;
    /**
     * Cancels a running background agent.
     * This aborts the underlying request via AbortSignal and marks the agent as cancelled.
     * @param agentId - The agent ID to cancel
     * @returns true if the agent was found and cancelled, false otherwise
     */
    cancel(agentId: string): boolean;
    /**
     * Removes a completed/finalized agent from the registry.
     * Only removes agents that are not running.
     * @param agentId - The agent ID to remove
     * @returns true if the agent was found and removed, false otherwise
     */
    remove(agentId: string): boolean;
    /**
     * Gets the count of running agents.
     */
    get runningCount(): number;
    /**
     * Gets the total count of tracked agents.
     */
    get totalCount(): number;
}

/**
 * Status of a background agent.
 */
declare type BackgroundAgentStatus = "running" | "completed" | "failed" | "cancelled";

/**
 * Unified type for background tasks, discriminated by `type` field.
 */
export declare type BackgroundTask = ShellTask | AgentTask;

/**
 * Discriminated union of progress information for background tasks.
 */
export declare type BackgroundTaskProgress = AgentTaskProgress | ShellTaskProgress;

/**
 * Status of a background task (agent or shell).
 */
export declare type BackgroundTaskStatus = "running" | "completed" | "failed" | "cancelled" | "killed";

/**
 * Base interface for all hook inputs
 */
export declare interface BaseHookInput {
    timestamp: number;
    cwd: string;
}

export declare abstract class BaseLogger implements RunnerLogger {
    protected logLevel?: LogLevel;
    protected debugEnvironmentVariables?: string[];
    private secretFilter;
    constructor(logLevel?: LogLevel, debugEnvironmentVariables?: string[]);
    filterSecrets(messageOrError: string | Error): string | Error;
    /**
     * Returns true if the log level is not set, or the log level is set and the level is enabled.
     */
    shouldLog(level: LogLevel): boolean;
    isDebug(): boolean;
    abstract log(message: string): void;
    abstract info(message: string): void;
    abstract debug(message: string): void;
    abstract notice(message: string | Error): void;
    abstract warning(message: string | Error): void;
    abstract error(message: string | Error): void;
    abstract startGroup(name: string, level?: LogLevel): void;
    abstract endGroup(level?: LogLevel): void;
}

declare type BasicToolConfig = {
    serverName: string;
    name: string;
    namespacedName: string;
    mcpServerName?: string;
    mcpToolName?: string;
    title: string;
    description: string;
    input_schema: ToolInputSchema;
    readOnly?: boolean;
    safeForTelemetry?: Tool_2["safeForTelemetry"] & Tool["safeForTelemetry"];
    filterMode?: ContentFilterMode;
};

declare type BinaryResult = {
    data: string;
    mimeType: string;
    type: string;
    /**
     * A description of the binary data.
     */
    description?: string;
};

declare type ChatCompletionMessageParamsWithToolCalls = Omit<ChatCompletionAssistantMessageParam, "tool_calls"> & {
    tool_calls?: CopilotChatCompletionMessageToolCall[];
    copilot_annotations?: unknown;
    /** Phase of generation for phased-output models (e.g. gpt-5.3-codex). */
    phase?: string;
};

/**
 * Information about a checkpoint for display in the prompt.
 */
declare interface CheckpointInfo {
    /** Checkpoint number (1-indexed) */
    number: number;
    /** Title of the checkpoint */
    title: string;
    /** Filename of the checkpoint (e.g., "001-plan-design.md") */
    filename: string;
}

export declare const CLI_CLIENT_NAME = "cli";

declare interface Client_2 {
    readonly model: string;
    getCompletionWithTools(systemMessage: string, initialMessages: ChatCompletionMessageParam[], tools: Tool_2[], options?: GetCompletionWithToolsOptions): AsyncGenerator<Event_2>;
}

declare interface ClientFactory {
    createClient(options: any, capabilities: any): Client;
}

/**
 * Connection information for an MCP client.
 */
declare type ClientInfo = {
    /**
     * Name of the MCP server this connection is for.
     */
    clientName: string;
    /**
     * Optional original/display name for this server.
     * This may contain "/" when the internal `clientName` has been adapted (e.g. "/" -> "__").
     */
    displayName?: string;
    /**
     * MCP client instance connected to the server.
     */
    mcpClient: Client;
    /**
     * Telemetry configuration for this connection.
     */
    safeForTelemetry?: Tool_2["safeForTelemetry"];
    /**
     * List of tools from the server to expose. ["*"] means all tools.
     */
    tools: string[];
    /**
     * Filter mode for the tools from this client.
     * If not specified, defaults to ContentFilterMode.HiddenCharacters.
     * If specified as a map, it applies to each tool by name.
     */
    filterMapping?: Record<string, ContentFilterMode> | ContentFilterMode;
    /**
     * Optional timeout in milliseconds for tool calls to this server.
     * If not specified, uses the default timeout.
     */
    timeout?: number;
    /**
     * Optional promise that resolves when the connection is established.
     * Used for deferred connections where the client is created immediately but
     * the connection is established in the background.
     */
    pendingConnection?: Promise<void>;
    /**
     * Whether this is the default Playwright server configured by the system,
     * as opposed to a user-provided Playwright server.
     */
    isDefaultServer?: boolean;
};

/**
 * Client information for telemetry events, matching Hydro's ClientInfo entity.
 */
declare interface ClientInfo_2 {
    cli_version: string;
    os_platform: string;
    os_arch: string;
    node_version: string;
    /** Type of client (e.g., "cli-interactive", "cli-prompt", "sdk") */
    client_type?: string;
    /** Name of the client application (e.g., "copilot-cli", "autopilot", "sdk") */
    client_name?: string;
    /** Whether the user is a GitHub/Msft staff member */
    is_staff?: boolean;
}

/**
 * The ideal set of options that a `{@link Client}` expose.
 */
declare type ClientOptions = {
    /**
     * The model to use for LLM completions.
     */
    model?: string;
    /**
     * The proportion of the model's input/prompt token limit
     * that should be given to tools as their token budget.
     */
    toolTokenBudgetProportion?: number;
    retryPolicy?: ClientRetryPolicy;
    /**
     * If for the current model, a higher level of thinking is possible, use it.
     * @default false
     */
    thinkingMode?: boolean;
    /**
     * The token budget for extended thinking/chain-of-thought for models that support it.
     * For Anthropic Claude models via CAPI, this maps to the `thinking_budget` parameter.
     * When set, enables extended thinking with the specified token budget.
     * This field remains optional even in ClientOptionsRequired since not all models support it.
     */
    thinkingBudget?: number | undefined;
    requestHeaders?: Record<string, string>;
    /**
     * If true, enables cache control checkpoints on messages sent to the model.
     * This allows downstream services to better manage caching of responses.
     * Defaults to false.
     */
    enableCacheControl?: boolean;
    /**
     * The default reasoning effort level for the model to use, if supported by the client.
     */
    defaultReasoningEffort?: ReasoningEffort;
    /**
     * The maximum number of output tokens for completions. When set, this value is sent
     * as `max_tokens` in the API request to cap the response length.
     */
    maxOutputTokens?: number;
    /**
     * Model family override for the agent. When set, uses the specified model family's
     * default configuration instead of looking up config by model name.
     */
    modelFamily?: string;
};

/**
 * Retry policies for the AI client.
 */
declare type ClientRetryPolicy = {
    /**
     * The maximum number of retries for **any** type of retryable failure or error.
     */
    maxRetries?: number;
    /**
     * Specific error codes that should always be retried.
     * - If a `number`, that specific error code will be retried.
     * - If a `[number, number]`, all error codes in the range will be retried (inclusive).
     * - If a `[number, undefined]`, all error codes greater than or equal to the first number will be retried.
     * - To retry all error codes based on an upper bound, simply use `[0, number]`.
     *
     * Some error codes are retried by default even if not specified here, for example 429 (rate limit exceeded).
     */
    errorCodesToRetry?: (number | [number, number | undefined])[];
    /**
     * How to handle retries for rate limiting (429) errors. If a policy is not provided, a default
     * policy will be used.
     */
    rateLimitRetryPolicy?: {
        /**
         * The default wait time in between retries if the server does not
         * provide a `retry-after` header.
         */
        defaultRetryAfterSeconds?: number;
        /**
         * The initial extra wait time in between retries. The extra wait time will
         * be added to the `retry-after` header value (or {@link defaultRetryAfterSeconds} if
         * the header is not present). After each retry, the extra wait time will grow beyond
         * this value according to the {@link retryBackoffExtraGrowth} factor.
         */
        initialRetryBackoffExtraSeconds?: number;
        /**
         * The growth factor for the retry backoff extra time. E.g. 2x, 3x, etc.
         */
        retryBackoffExtraGrowth?: number;
        /**
         * The maximum wait time in between retries.
         */
        maxRetryAfterSeconds?: number;
    };
};

/** Diagnostic stats exposed by {@link CoalescingWriteQueue.stats}. */
declare interface CoalescingWriteQueueStats {
    writeCount: number;
    pendingLength: number;
    drainScheduled: boolean;
}

/**
 * Code change metrics tracked during a session
 */
export declare interface CodeChangeMetrics {
    linesAdded: number;
    linesRemoved: number;
    filesModified: Set<string>;
}

declare type Command = {
    readonly identifier: string;
    readonly readOnly: boolean;
};

declare type CompactionCompletedEvent = {
    kind: "compaction_completed";
    turn: number;
    performedBy: string;
    success: boolean;
    error?: string;
    compactionResult?: CompactionEventResult;
};

declare type CompactionEvent = CompactionStartedEvent | CompactionCompletedEvent;

declare type CompactionEventResult = {
    tokenLimit: number;
    preCompactionTokens: number;
    preCompactionMessagesLength: number;
    postCompactionTokens?: number;
    postCompactionMessagesLength?: number;
    tokensRemoved?: number;
    messagesRemoved?: number;
    summaryContent: string;
    checkpointNumber?: number;
    compactionTokensUsed?: {
        input: number;
        output: number;
        cachedInput: number;
    };
};

/**
 * Result of a conversation history compaction operation.
 */
export declare interface CompactionResult {
    success: boolean;
    tokensRemoved: number;
    messagesRemoved: number;
    summaryContent: string;
}

declare type CompactionStartedEvent = {
    kind: "compaction_started";
    turn: number;
    performedBy: string;
};

export declare function completeOrphanedToolCalls(messages: ChatCompletionMessageParam[]): ChatCompletionMessageParam[];

declare type CompletionWithToolsModel = {
    readonly name: string;
    readonly id?: string;
    readonly capabilities?: {
        readonly supports?: {
            readonly vision?: boolean;
        };
        readonly limits?: {
            readonly max_prompt_tokens?: number;
            readonly max_output_tokens?: number;
            readonly max_context_window_tokens?: number;
            readonly vision?: {
                readonly supported_media_types: string[];
                readonly max_prompt_images: number;
                readonly max_prompt_image_size: number;
            };
        };
    };
};

export declare class CompoundLogger implements RunnerLogger {
    readonly loggers: RunnerLogger[];
    constructor(loggers: RunnerLogger[]);
    isDebug(): boolean;
    debug(message: string): void;
    log(message: string): void;
    info(message: string): void;
    notice(message: string | Error): void;
    warning(message: string | Error): void;
    error(message: string | Error): void;
    startGroup(name: string, level?: LogLevel): void;
    endGroup(level?: LogLevel): void;
}

export declare class ConsoleLogger extends BaseLogger implements RunnerLogger {
    constructor(logLevel?: LogLevel, debugEnvironmentVariables?: string[]);
    log(message: string): void;
    debug(message: string): void;
    info(message: string): void;
    notice(message: string | Error): void;
    warning(message: string | Error): void;
    error(message: string | Error): void;
    startGroup(name: string, level?: LogLevel): void;
    endGroup(level?: LogLevel): void;
}

export declare type ContentBlock = z_2.infer<typeof ContentBlockSchema>;

/**
 * Content block union type
 */
declare const ContentBlockSchema: z_2.ZodUnion<[z_2.ZodObject<{
    type: z_2.ZodLiteral<"text">;
    text: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    type: "text";
    text: string;
}, {
    type: "text";
    text: string;
}>, z_2.ZodObject<{
    type: z_2.ZodLiteral<"terminal">;
    text: z_2.ZodString;
    exitCode: z_2.ZodOptional<z_2.ZodNumber>;
    cwd: z_2.ZodOptional<z_2.ZodString>;
}, "strip", z_2.ZodTypeAny, {
    type: "terminal";
    text: string;
    exitCode?: number | undefined;
    cwd?: string | undefined;
}, {
    type: "terminal";
    text: string;
    exitCode?: number | undefined;
    cwd?: string | undefined;
}>, z_2.ZodObject<{
    type: z_2.ZodLiteral<"image">;
    data: z_2.ZodString;
    mimeType: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    data: string;
    type: "image";
    mimeType: string;
}, {
    data: string;
    type: "image";
    mimeType: string;
}>, z_2.ZodObject<{
    type: z_2.ZodLiteral<"audio">;
    data: z_2.ZodString;
    mimeType: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    data: string;
    type: "audio";
    mimeType: string;
}, {
    data: string;
    type: "audio";
    mimeType: string;
}>, z_2.ZodObject<{
    icons: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
        src: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        sizes: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
        theme: z_2.ZodOptional<z_2.ZodEnum<["light", "dark"]>>;
    }, "strip", z_2.ZodTypeAny, {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }, {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }>, "many">>;
    name: z_2.ZodString;
    title: z_2.ZodOptional<z_2.ZodString>;
    uri: z_2.ZodString;
    description: z_2.ZodOptional<z_2.ZodString>;
    mimeType: z_2.ZodOptional<z_2.ZodString>;
    size: z_2.ZodOptional<z_2.ZodNumber>;
} & {
    type: z_2.ZodLiteral<"resource_link">;
}, "strip", z_2.ZodTypeAny, {
    name: string;
    type: "resource_link";
    uri: string;
    mimeType?: string | undefined;
    size?: number | undefined;
    description?: string | undefined;
    title?: string | undefined;
    icons?: {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }[] | undefined;
}, {
    name: string;
    type: "resource_link";
    uri: string;
    mimeType?: string | undefined;
    size?: number | undefined;
    description?: string | undefined;
    title?: string | undefined;
    icons?: {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }[] | undefined;
}>, z_2.ZodObject<{
    type: z_2.ZodLiteral<"resource">;
    resource: z_2.ZodUnion<[z_2.ZodObject<{
        uri: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        text: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    }, {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    }>, z_2.ZodObject<{
        uri: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        blob: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    }, {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    }>]>;
}, "strip", z_2.ZodTypeAny, {
    type: "resource";
    resource: {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    } | {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    };
}, {
    type: "resource";
    resource: {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    } | {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    };
}>]>;

/**
 * Service for checking content exclusion rules.
 *
 * This service fetches exclusion rules from the GitHub API and caches them.
 * It provides methods to check if files should be excluded based on:
 * - Path patterns (glob matching)
 * - Content patterns (regex matching via ifAnyMatch/ifNoneMatch)
 *
 * Usage:
 * 1. Create an instance with auth info
 * 2. Call startFetching() at session start (non-blocking)
 * 3. Call isExcluded() when checking files (blocks until fetch complete if needed)
 */
declare class ContentExclusionService {
    private readonly host;
    private readonly token;
    private readonly featureEnabled;
    /** Promise for the initial fetch, awaited on first access */
    private fetchPromise;
    /** Whether the initial fetch failed (used to block access when copilotignore is enabled) */
    private fetchFailed;
    /** Directories passed to startFetching, used for cache refresh */
    private fetchDirectories;
    /** Cached rules per repository (keyed by normalized remote URL) */
    private rulesCache;
    /** Cache mapping absolute file paths to their git repo root (caches the promise to deduplicate parallel calls) */
    private gitRootCache;
    /** Cache mapping git repo roots to their normalized remote URLs (caches the promise to deduplicate parallel calls) */
    private repoUrlCache;
    /** Optional callback for emitting telemetry events */
    private readonly onTelemetry?;
    constructor(host: string, token: string, featureEnabled: boolean, onTelemetry?: (event: TelemetryEvent_2) => void);
    /**
     * Starts fetching exclusion rules for repositories in the given directories.
     * This method is non-blocking - it kicks off the fetch but doesn't wait.
     *
     * @param directories Directories to scan for git repositories
     */
    startFetching(directories: string[]): void;
    /**
     * Returns whether any content exclusion rules are active.
     * Awaits the initial fetch if still in progress.
     */
    hasRules(): Promise<boolean>;
    /**
     * Checks if a file is excluded by content exclusion rules.
     *
     * This method will block on the initial fetch if it hasn't completed yet.
     *
     * @param absolutePath Absolute path to the file
     * @returns Exclusion check result with excluded status and reason
     */
    isExcluded(absolutePath: string): Promise<ExclusionCheckResult>;
    /**
     * Checks if any files in a list are excluded.
     * Useful for shell command checking via possiblePaths.
     *
     * @param absolutePaths Array of absolute file paths
     * @returns First excluded path and its result, or null if none excluded
     */
    findFirstExcluded(absolutePaths: string[]): Promise<{
        path: string;
        result: ExclusionCheckResult;
    } | null>;
    /**
     * Gets the formatted error message for an excluded file.
     */
    getExclusionMessage(filePath: string): string;
    /**
     * Fetches rules for all git repositories (including submodules) found in the given directories.
     */
    private fetchRulesForDirectories;
    private emitRulesFetchedTelemetry;
    /**
     * Fetches wildcard rules that apply to files outside git repositories.
     */
    private fetchWildcardRules;
    /**
     * Gets normalized remote URLs for a directory's git repository.
     */
    private getRepoUrlsForDirectory;
    /**
     * Discovers submodule remote URLs for all git roots found in the given directories.
     * The content exclusion API requires submodule URLs to be supplied explicitly.
     */
    private discoverSubmoduleUrls;
    /**
     * Caches rules from an API response.
     */
    private cacheRules;
    /**
     * Checks if the cache for a key is still valid.
     */
    private isCacheValid;
    /**
     * Gets the git root for a file path, using cache.
     */
    private getGitRootForPath;
    /**
     * Core exclusion check logic.
     */
    private checkExclusion;
    /**
     * Checks content-based exclusion rules (ifAnyMatch/ifNoneMatch).
     * Returns true if the file should be excluded based on content.
     */
    private checkContentRules;
    /**
     * Refreshes the cache if TTL has expired.
     * Called automatically during exclusion checks for long-running sessions.
     */
    private refreshIfNeeded;
}

declare enum ContentFilterMode {
    None = "none",
    Markdown = "markdown",
    HiddenCharacters = "hidden_characters"
}

/** Represents direct Copilot API authentication (via GITHUB_COPILOT_API_TOKEN + COPILOT_API_URL). */
declare type CopilotApiTokenAuthInfo = {
    readonly type: "copilot-api-token";
    readonly host: "https://github.com";
    readonly copilotUser?: CopilotUserResponse;
};

/**
 * Note: agent sessions API depend on this type!
 */
declare type CopilotChatCompletionChunk = Omit<ChatCompletionChunk, "choices"> & {
    choices: CopilotChatCompletionChunkChoices;
};

declare type CopilotChatCompletionChunkChoice = Omit<ChatCompletionChunk.Choice, "delta"> & {
    delta: CopilotChatCompletionChunkDelta;
};

declare type CopilotChatCompletionChunkChoices = Array<CopilotChatCompletionChunkChoice>;

declare type CopilotChatCompletionChunkDelta = Omit<ChatCompletionChunk.Choice.Delta, "tool_calls"> & ReasoningMessageParam & {
    tool_calls?: Array<CopilotChatCompletionToolCallDelta>;
    copilot_annotations?: string | undefined;
    /** Phase of generation for phased-output models (e.g. gpt-5.3-codex). */
    phase?: string;
};

/**
 * Re-export the OpenAI union type for convenience.
 * ChatCompletionMessageToolCall = ChatCompletionMessageFunctionToolCall | ChatCompletionMessageCustomToolCall
 */
declare type CopilotChatCompletionMessageToolCall = ChatCompletionMessageToolCall & {
    index?: number;
};

/**
 * Streaming tool call delta that supports both function and custom tool calls.
 */
declare type CopilotChatCompletionToolCallDelta = FunctionToolCallDelta | CustomToolCallDelta;

declare interface CopilotSessionsClient {
    sessionId(): string;
    error(error: Error): Promise<void>;
    log(logs: SessionLogsContent): Promise<void>;
    logNonCompletionContent(content: string): Promise<void>;
    createOrUpdateMCPStartupToolCall(params: {
        content?: string;
        serverName: string;
        toolNamesToDisplayNames?: Record<string, string>;
    }): Promise<void>;
    logTitleAndBody(title: string, body: string, agentId?: string): Promise<void>;
    getLogs(sessionId: string): Promise<SessionLogsContents | string | undefined>;
}

declare type CopilotUserResponse = z_2.infer<typeof copilotUserResponseSchema>;

declare const copilotUserResponseSchema: z_2.ZodObject<{
    login: z_2.ZodOptional<z_2.ZodString>;
    access_type_sku: z_2.ZodOptional<z_2.ZodString>;
    analytics_tracking_id: z_2.ZodOptional<z_2.ZodString>;
    assigned_date: z_2.ZodNullable<z_2.ZodOptional<z_2.ZodString>>;
    can_signup_for_limited: z_2.ZodOptional<z_2.ZodBoolean>;
    chat_enabled: z_2.ZodOptional<z_2.ZodBoolean>;
    copilot_plan: z_2.ZodOptional<z_2.ZodString>;
    copilotignore_enabled: z_2.ZodOptional<z_2.ZodBoolean>;
    endpoints: z_2.ZodOptional<z_2.ZodObject<{
        api: z_2.ZodOptional<z_2.ZodString>;
        "origin-tracker": z_2.ZodOptional<z_2.ZodString>;
        proxy: z_2.ZodOptional<z_2.ZodString>;
        telemetry: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        api?: string | undefined;
        telemetry?: string | undefined;
        "origin-tracker"?: string | undefined;
        proxy?: string | undefined;
    }, {
        api?: string | undefined;
        telemetry?: string | undefined;
        "origin-tracker"?: string | undefined;
        proxy?: string | undefined;
    }>>;
    organization_login_list: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
    organization_list: z_2.ZodNullable<z_2.ZodOptional<z_2.ZodArray<z_2.ZodNullable<z_2.ZodObject<{
        login: z_2.ZodNullable<z_2.ZodOptional<z_2.ZodString>>;
        name: z_2.ZodNullable<z_2.ZodOptional<z_2.ZodString>>;
    }, "strip", z_2.ZodTypeAny, {
        name?: string | null | undefined;
        login?: string | null | undefined;
    }, {
        name?: string | null | undefined;
        login?: string | null | undefined;
    }>>, "many">>>;
    codex_agent_enabled: z_2.ZodOptional<z_2.ZodBoolean>;
    quota_reset_date: z_2.ZodOptional<z_2.ZodString>;
    quota_snapshots: z_2.ZodOptional<z_2.ZodObject<{
        chat: z_2.ZodOptional<z_2.ZodObject<{
            entitlement: z_2.ZodOptional<z_2.ZodNumber>;
            overage_count: z_2.ZodOptional<z_2.ZodNumber>;
            overage_permitted: z_2.ZodOptional<z_2.ZodBoolean>;
            percent_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            quota_id: z_2.ZodOptional<z_2.ZodString>;
            quota_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            remaining: z_2.ZodOptional<z_2.ZodNumber>;
            unlimited: z_2.ZodOptional<z_2.ZodBoolean>;
            timestamp_utc: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }>>;
        completions: z_2.ZodOptional<z_2.ZodObject<{
            entitlement: z_2.ZodOptional<z_2.ZodNumber>;
            overage_count: z_2.ZodOptional<z_2.ZodNumber>;
            overage_permitted: z_2.ZodOptional<z_2.ZodBoolean>;
            percent_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            quota_id: z_2.ZodOptional<z_2.ZodString>;
            quota_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            remaining: z_2.ZodOptional<z_2.ZodNumber>;
            unlimited: z_2.ZodOptional<z_2.ZodBoolean>;
            timestamp_utc: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }>>;
        premium_interactions: z_2.ZodOptional<z_2.ZodObject<{
            entitlement: z_2.ZodOptional<z_2.ZodNumber>;
            overage_count: z_2.ZodOptional<z_2.ZodNumber>;
            overage_permitted: z_2.ZodOptional<z_2.ZodBoolean>;
            percent_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            quota_id: z_2.ZodOptional<z_2.ZodString>;
            quota_remaining: z_2.ZodOptional<z_2.ZodNumber>;
            remaining: z_2.ZodOptional<z_2.ZodNumber>;
            unlimited: z_2.ZodOptional<z_2.ZodBoolean>;
            timestamp_utc: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }, {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        chat?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        completions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        premium_interactions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
    }, {
        chat?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        completions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        premium_interactions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
    }>>;
    quota_reset_date_utc: z_2.ZodOptional<z_2.ZodString>;
}, "strip", z_2.ZodTypeAny, {
    login?: string | undefined;
    access_type_sku?: string | undefined;
    analytics_tracking_id?: string | undefined;
    assigned_date?: string | null | undefined;
    can_signup_for_limited?: boolean | undefined;
    chat_enabled?: boolean | undefined;
    copilot_plan?: string | undefined;
    copilotignore_enabled?: boolean | undefined;
    endpoints?: {
        api?: string | undefined;
        telemetry?: string | undefined;
        "origin-tracker"?: string | undefined;
        proxy?: string | undefined;
    } | undefined;
    organization_login_list?: string[] | undefined;
    organization_list?: ({
        name?: string | null | undefined;
        login?: string | null | undefined;
    } | null)[] | null | undefined;
    codex_agent_enabled?: boolean | undefined;
    quota_reset_date?: string | undefined;
    quota_snapshots?: {
        chat?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        completions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        premium_interactions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
    } | undefined;
    quota_reset_date_utc?: string | undefined;
}, {
    login?: string | undefined;
    access_type_sku?: string | undefined;
    analytics_tracking_id?: string | undefined;
    assigned_date?: string | null | undefined;
    can_signup_for_limited?: boolean | undefined;
    chat_enabled?: boolean | undefined;
    copilot_plan?: string | undefined;
    copilotignore_enabled?: boolean | undefined;
    endpoints?: {
        api?: string | undefined;
        telemetry?: string | undefined;
        "origin-tracker"?: string | undefined;
        proxy?: string | undefined;
    } | undefined;
    organization_login_list?: string[] | undefined;
    organization_list?: ({
        name?: string | null | undefined;
        login?: string | null | undefined;
    } | null)[] | null | undefined;
    codex_agent_enabled?: boolean | undefined;
    quota_reset_date?: string | undefined;
    quota_snapshots?: {
        chat?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        completions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
        premium_interactions?: {
            entitlement?: number | undefined;
            overage_count?: number | undefined;
            overage_permitted?: boolean | undefined;
            percent_remaining?: number | undefined;
            quota_id?: string | undefined;
            quota_remaining?: number | undefined;
            remaining?: number | undefined;
            unlimited?: boolean | undefined;
            timestamp_utc?: string | undefined;
        } | undefined;
    } | undefined;
    quota_reset_date_utc?: string | undefined;
}>;

/**
 * Counts the total number of hooks in a QueryHooks object.
 */
export declare function countHooks(hooks: QueryHooks | undefined): number;

/**
 * Create an empty metrics object.
 */
export declare function createEmptyMetrics(sessionStartTime?: number): UsageMetrics;

/**
 * Type for a custom tool call delta (streaming).
 */
declare type CustomToolCallDelta = {
    index: number;
    id?: string;
    type?: "custom";
    custom?: {
        name?: string;
        input?: string;
    };
};

/**
 * Format specification for custom tools that use grammar-based input.
 */
declare type CustomToolInputFormat = {
    /**
     * The type of format. Currently only "grammar" is supported.
     */
    type: "grammar";
    /**
     * The syntax of the grammar (e.g., "lark").
     */
    syntax: string;
    /**
     * The grammar definition.
     */
    definition: string;
};

declare type DeepPartial<T> = {
    [P in keyof T]?: T[P] extends (infer U)[] ? U[] : T[P] extends object ? DeepPartial<T[P]> : T[P];
};

export declare const DEFAULT_INTEGRATION_ID = "copilot-developer-cli";

/**
 * Represents a detached shell session tracked in the global registry.
 * Detached sessions are shell processes that persist independently of the agent session.
 */
declare interface DetachedShell {
    /** Unique identifier for this shell session */
    shellId: string;
    /** Short description of what the command does */
    description: string;
    /** The command being executed */
    command: string;
    /** Process ID of the detached process (read from .pid file) */
    pid?: number;
    /** When the shell was started */
    startedAt: number;
    /** When the shell completed (if completed) */
    completedAt?: number;
    /** Path to the log file for this detached process */
    logPath: string;
    /** Current status of the shell */
    status: DetachedShellStatus;
}

/**
 * Global registry for tracking detached shell sessions.
 * This singleton allows the CLI to access detached shell information
 * that would otherwise be trapped inside the InteractiveShellToolContext.
 */
declare class DetachedShellRegistry {
    private readonly shells;
    private onChangeCallback?;
    /**
     * Set a callback to be notified when shells are registered or complete.
     * @param callback - Function to call when shells change
     */
    setOnChangeCallback(callback: () => void): void;
    /**
     * Registers a detached shell session.
     * Called by InteractiveShellToolContext when a detached process is started.
     */
    register(shell: Omit<DetachedShell, "status">): void;
    /**
     * Schedules a refresh attempt for a shell, with retry logic if PID isn't found.
     */
    private scheduleRefresh;
    /**
     * Refreshes the PID and status for a shell.
     * @returns true if the shell has a PID (either already had one or successfully read it)
     */
    refreshShell(shellId: string): Promise<boolean>;
    /**
     * Refreshes all shells' status.
     */
    refreshAll(): Promise<void>;
    /**
     * Unregisters a detached shell session.
     * Called when a detached process is killed or completes.
     */
    unregister(shellId: string): void;
    /**
     * Removes a completed/finalized shell from the registry.
     * Only removes shells that are not running.
     * @param shellId - The shell ID to remove
     * @returns true if the shell was found and removed, false otherwise
     */
    remove(shellId: string): boolean;
    /**
     * Gets a specific detached shell by ID.
     */
    get(shellId: string): DetachedShell | undefined;
    /**
     * Lists all detached shell sessions.
     */
    list(): DetachedShell[];
    /**
     * Gets the count of detached shells.
     */
    get count(): number;
    /**
     * Gets recent output and PID for a detached shell.
     * Reads the last 10 lines from the log file (seeking from end) and the PID from the .pid file.
     */
    getProgress(logPath: string): Promise<{
        recentOutput: string;
        pid?: number;
    }>;
    /**
     * Kills a detached shell process by sending SIGTERM to its PID.
     * Returns true if the kill signal was sent, false if the process wasn't found.
     */
    kill(shellId: string): Promise<boolean>;
}

/**
 * Status of a detached shell session.
 */
declare type DetachedShellStatus = "running" | "completed" | "killed";

declare interface Disposable_2 {
    dispose(): void | Promise<void>;
}

export declare type EmbeddedResource = z_2.infer<typeof EmbeddedResourceSchema>;

/**
 * Embedded resource content block
 */
declare const EmbeddedResourceSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"resource">;
    resource: z_2.ZodUnion<[z_2.ZodObject<{
        uri: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        text: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    }, {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    }>, z_2.ZodObject<{
        uri: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        blob: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    }, {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    }>]>;
}, "strip", z_2.ZodTypeAny, {
    type: "resource";
    resource: {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    } | {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    };
}, {
    type: "resource";
    resource: {
        text: string;
        uri: string;
        mimeType?: string | undefined;
    } | {
        uri: string;
        blob: string;
        mimeType?: string | undefined;
    };
}>;

/**
 * Enables a model's policy for the current user.
 * This is used for models that are available but require user consent (policy.state !== 'enabled').
 */
export declare function enableModelPolicy(authInfo: AuthInfo, modelId: string, integrationId?: string, sessionId?: string, logger?: RunnerLogger): Promise<EnableModelPolicyResult>;

export declare type EnableModelPolicyResult = {
    success: boolean;
    error?: string;
    canBeEnabled?: boolean;
};

/** Represents the Personal Access Token (PAT) or server-to-server token authentication information. */
declare type EnvAuthInfo = {
    readonly type: "env";
    readonly host: string;
    /** The login of the user. Undefined for server-to-server tokens (ghs_). */
    readonly login?: string;
    readonly token: string;
    readonly envVar: string;
    readonly copilotUser?: CopilotUserResponse;
};

/**
 * EnvValueMode controls how the value of a key/value pair in the `env` block of an MCP server config is
 * interpreted. `direct` means the value is used as-is. `indirect` means the value is the name
 * of an environment variable whose value should be used.
 */
declare type EnvValueMode = "direct" | "indirect";

export declare type ErrorOccurredHook = (input: ErrorOccurredHookInput) => Promise<ErrorOccurredHookOutput | void>;

/**
 * Error occurred hook types
 */
export declare interface ErrorOccurredHookInput extends BaseHookInput {
    error: Error;
    errorContext: "model_call" | "tool_execution" | "system" | "user_input";
    recoverable: boolean;
}

export declare interface ErrorOccurredHookOutput {
    suppressOutput?: boolean;
    errorHandling?: "retry" | "skip" | "abort";
    retryCount?: number;
    userNotification?: string;
}

declare type Event_2 = MessageEvent_2 | ResponseEvent | ModelCallFailureEvent | TurnEvent | TruncationEvent | UsageInfoEvent | CompactionEvent | ImageProcessingEvent | ImageRemovalEvent | ModelCallSuccessEvent | ToolExecutionEvent | TelemetryEvent;

declare type EventData<T extends EventType> = EventPayload<T>["data"];

declare type EventHandler<T extends EventType> = (event: EventPayload<T>) => void;

declare type EventPayload<T extends EventType> = Extract<SessionEvent, {
    type: T;
}>;

/**
 * Telemetry can be emitted by the runtime via progress events. Telemetry is attached to progress events
 * on a `telemetry` property whose type is this.
 */
declare type EventTelemetry<EventT = string, TelemetryT extends Telemetry = Telemetry> = {
    /**
     * The name of the telemetry event associated with the emitted runtime progress event.
     */
    event: EventT;
    /**
     * String-esque properties that are associated with the telemetry event.
     * WARNING: Do not put sensitive data here. Use restrictedProperties for that.
     */
    properties: TelemetryT["properties"];
    /**
     * String-esque properties that are associated with the telemetry event. These are only available on the restricted topics
     */
    restrictedProperties: TelemetryT["restrictedProperties"];
    /**
     * Number-esque metrics that are associated with the telemetry event. Both integer and floating point values are possible.
     */
    metrics: TelemetryT["metrics"];
};

declare type EventType = SessionEvent["type"];

/**
 * Result of checking if a file is excluded.
 */
declare interface ExclusionCheckResult {
    /** Whether the file is excluded */
    excluded: boolean;
    /** If excluded, the reason/source */
    reason?: string;
    /** If excluded, the source that set the rule */
    source?: {
        name: string;
        type: "Repository" | "Organization";
    };
}

export declare function executeHooks<TInput extends BaseHookInput, TOutput>(hooks: ((input: TInput) => Promise<TOutput | void>)[] | undefined, input: TInput, logger: RunnerLogger): Promise<void | TOutput>;

export declare interface ExternalToolDefinition {
    name: string;
    description: string;
    parameters?: Record<string, unknown>;
}

export declare type ExternalToolDispatcher = (request: ExternalToolInvocation) => Promise<ToolResult>;

export declare interface ExternalToolInvocation {
    sessionId: string;
    toolCallId: string;
    toolName: string;
    arguments: unknown;
    abortSignal?: AbortSignal;
}

declare type FeatureFlag = keyof FeatureFlagAvailabilityMap;

/**
 * Internal feature flag availability definitions. Defines which tier each flag belongs to.
 *
 * @internal Exported for testing only
 */
declare const featureFlagAvailability: {
    readonly CUSTOM_AGENTS: "on";
    readonly CCA_DELEGATE: "on";
    readonly CONTINUITY: "on";
    readonly CONTINUITY_REMOTE_CONTROL: "staff";
    readonly FEATURE_FLAG_TEST: "staff";
    readonly S2STOKENS: "staff";
    readonly "copilot-feature-agentic-memory": "on";
    readonly PLAN_COMMAND: "on";
    readonly PERSISTED_PERMISSIONS: "staff-or-experimental";
    readonly PLUGIN_COMMAND: "on";
    readonly COPILOT_SWE_AGENT_BACKGROUND_AGENTS: "on";
    readonly COPILOT_SWE_AGENT_PARALLEL_TASK_EXECUTION: "on";
    readonly AUTOPILOT_MODE: "staff-or-experimental";
    readonly QUEUED_COMMANDS: "staff";
    readonly DISABLE_WEB_TOOLS: "off";
    readonly FLEET_COMMAND: "staff-or-experimental";
    readonly LSP_TOOLS: "on";
    readonly CONTENT_EXCLUSION: "staff";
    readonly SKILLS_INSTRUCTIONS: "staff";
    readonly IMAGE_PASTE_FALLBACK: "staff-or-experimental";
    readonly ADO_DETECTION: "staff";
    readonly SESSION_CLEANUP: "staff-or-experimental";
    readonly TUIKIT_COMMAND: "off";
    readonly CHILD_CUSTOM_INSTRUCTIONS: "staff";
    readonly SUBAGENT_COMPACTION: "staff-or-experimental";
    readonly DIAGNOSE: "staff";
    readonly UPLOAD_DEBUG_LOGS: "staff";
};

declare type FeatureFlagAvailabilityMap = typeof featureFlagAvailability;

/** Resolved feature flags with boolean values for runtime use */
declare type FeatureFlags = Readonly<Record<FeatureFlag, boolean>>;

export declare class FileLogger extends BaseLogger implements RunnerLogger, LogWriter {
    private readonly filePath;
    private readonly queue;
    constructor(filePath: string, logLevel?: LogLevel, debugEnvironmentVariables?: string[]);
    /** Promise that resolves when pending writes are complete. Used to serialize
     * writes and for testing. */
    get writeQueue(): Promise<void>;
    outputPath(): string;
    log(message: string): void;
    debug(message: string): void;
    info(message: string): void;
    notice(message: string | Error): void;
    warning(message: string | Error): void;
    error(message: string | Error): void;
    startGroup(name: string, level?: LogLevel): void;
    endGroup(level?: LogLevel): void;
    /** @internal Exported for testing only. */
    get stats(): CoalescingWriteQueueStats;
    write(category: string, message: string): void;
    /** Implementation for LogWriter interface */
    writeLog(level: LogLevel_2, message: string): Promise<void>;
}

/**
 * Type for a function tool call delta (streaming).
 */
declare type FunctionToolCallDelta = {
    index: number;
    id?: string;
    type?: "function";
    function?: {
        name?: string;
        arguments?: string;
    };
};

/**
 * Retrieves the list of available models based on policies and integration including capabilities and
 * billing information, which may be cached from previous calls.
 *
 * This list is in order of preference to be the default model for new sessions where the first model is
 * the most preferred.  It can be empty if all model policies are disabled.
 *
 * @deprecated Used solely by vscode-copilot-chat extension, use {@link retrieveAvailableModels} instead.
 */
export declare function getAvailableModels(authInfo: AuthInfo): Promise<Model[]>;

declare type GetCompletionWithToolsOptions = {
    /**
     * If `true`, then calls do `getCompletionWithTools` will check if the token counts of
     * the initial system messages, user messages, and tool definitions, exceed the limits
     * of the model. If they do, then the call will throw an error. If `false`, then the
     * call will not perform any checks.
     *
     * Defaults to `false`.
     */
    failIfInitialInputsTooLong?: boolean;
    toolChoice?: ChatCompletionToolChoiceOption;
    requestHeaders?: Record<string, string>;
    /**
     * If true, performs the request in streaming mode. This results in additional events
     * as each chunk is received from the service.
     */
    stream?: boolean;
    /**
     * If this call is a continuation of a previous `getCompletionWithTools` call, this specifies what turn
     * that conversation was/is on. This is used to determine the initial turn count in this
     * call to `getCompletionWithTools`.
     */
    initialTurnCount?: number;
    /**
     * If `true`, then the client will execute tools in parallel if model supports `parallel_tool_calls`.
     * If `false`, then the client will execute tools sequentially even if the model returns multiple tool calls.
     * Defaults to false.
     */
    executeToolsInParallel?: boolean;
    /**
     * Processors provide a way to do work during different stages of the completion with tools
     * lifecycle. Processors will be called in the order they are provided.
     */
    processors?: {
        preRequest?: IPreRequestProcessor[];
        postRequest?: IPostRequestProcessor[];
        onRequestError?: IOnRequestErrorProcessor[];
        preToolsExecution?: IPreToolsExecutionProcessor[];
        postToolExecution?: IPostToolExecutionProcessor[];
        onStreamingChunk?: IOnStreamingChunkProcessor[];
    };
    /**
     * Signal to abort the completion request.
     */
    abortSignal?: AbortSignal;
    /**
     * An optional identifier for the completion with tools call. This can be used for logging
     * and tracing purposes.
     */
    callId?: string;
    /**
     * The reasoning effort level for the model to use, if supported by the client.
     */
    reasoningEffort?: ReasoningEffort;
};

/** Gets the list of available custom agents. */
export declare function getCustomAgents(authInfo: AuthInfo, workingDir: string, integrationId?: string, logger?: RunnerLogger, settings?: RuntimeSettings): Promise<SweCustomAgent[]>;

/** Represents the GitHub CLI authentication information. */
declare type GhCliAuthInfo = {
    readonly type: "gh-cli";
    readonly host: string;
    readonly login: string;
    readonly token: string;
    readonly copilotUser?: CopilotUserResponse;
};

declare interface HandoffProgress {
    step: HandoffStep;
    status: "in-progress" | "complete";
    message?: string;
}

declare type HandoffStep = "load-session" | "validate-repo" | "check-changes" | "checkout-branch" | "create-session" | "save-session";

/** Represents the HMAC-based authentication information. */
declare type HMACAuthInfo = {
    readonly type: "hmac";
    readonly host: "https://github.com";
    readonly hmac: string;
    readonly copilotUser?: CopilotUserResponse;
};

export declare type HookEndEvent = z_2.infer<typeof HookEndEventSchema>;

/**
 * Hook invocation completes
 */
declare const HookEndEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"hook.end">;
    data: z_2.ZodObject<{
        hookInvocationId: z_2.ZodString;
        hookType: z_2.ZodString;
        output: z_2.ZodUnknown;
        success: z_2.ZodBoolean;
        error: z_2.ZodOptional<z_2.ZodObject<{
            message: z_2.ZodString;
            stack: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            message: string;
            stack?: string | undefined;
        }, {
            message: string;
            stack?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    }, {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    };
    id: string;
    type: "hook.end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    };
    id: string;
    type: "hook.end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type HookStartEvent = z_2.infer<typeof HookStartEventSchema>;

/**
 * Hook invocation begins
 */
declare const HookStartEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"hook.start">;
    data: z_2.ZodObject<{
        hookInvocationId: z_2.ZodString;
        hookType: z_2.ZodString;
        input: z_2.ZodUnknown;
    }, "strip", z_2.ZodTypeAny, {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    }, {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    };
    id: string;
    type: "hook.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    };
    id: string;
    type: "hook.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare interface HTTPTransportConfig {
    type: "http";
    url: string;
    headers?: Record<string, string>;
    authProvider?: OAuthClientProvider;
}

/**
 * Union type for all Hydro events that can be sent.
 */
declare type HydroEvent = {
    name: "cli.telemetry";
    event: HydroTelemetry;
} | {
    name: "cli.model_call";
    event: HydroModelCall;
} | {
    name: "cli.tool_call";
    event: HydroToolCall;
} | {
    name: "cli.restricted_telemetry";
    event: HydroRestrictedTelemetry;
} | {
    name: "cli.restricted_model_call";
    event: HydroRestrictedModelCall;
} | {
    name: "cli.restricted_tool_call";
    event: HydroRestrictedToolCall;
};

/**
 * Structured model call telemetry event.
 * Maps to: copilot_cli.v0.ModelCall (model_call.proto)
 */
declare interface HydroModelCall {
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** API response ID (e.g., chatcmpl-xxx from OpenAI). Referenced by other events as model_call_id */
    api_id?: string;
    /** Model used for this call */
    model: string;
    /** Completion token count */
    completion_tokens_count?: number;
    /** Prompt token count */
    prompt_tokens_count?: number;
    /** Total token count */
    total_tokens_count?: number;
    /** Cached tokens count */
    cached_tokens_count?: number;
    /** Number of tool calls in this response */
    tool_calls_count?: number;
    /** Duration measured internally (not from API) in milliseconds */
    duration_ms?: number;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID */
    copilot_tracking_id?: string;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** GitHub request tracing ID */
    request_id?: string;
    /** HTTP status of model call response */
    status?: number;
    /** Feature flags enabled at time of this call */
    features?: Record<string, string>;
    /** Client environment metadata */
    client?: ClientInfo_2;
}

/**
 * Restricted model call telemetry event with sensitive content.
 * Maps to: copilot_cli.v0.RestrictedModelCall (restricted_model_call.proto)
 */
declare interface HydroRestrictedModelCall {
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** Reported created timestamp from API payload (ISO 8601 format) */
    api_created_at?: string;
    /** API response ID (e.g., chatcmpl-xxx from OpenAI). Referenced by other events as model_call_id */
    api_id?: string;
    /** Model used for this call */
    model: string;
    /** Completion token count */
    completion_tokens_count?: number;
    /** Prompt token count */
    prompt_tokens_count?: number;
    /** Total token count */
    total_tokens_count?: number;
    /** Cached tokens count */
    cached_tokens_count?: number;
    /** Number of tool calls in this response */
    tool_calls_count?: number;
    /** Duration measured internally (not from API) in milliseconds */
    duration_ms?: number;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID */
    copilot_tracking_id?: string;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** GitHub request tracing ID */
    request_id?: string;
    /** HTTP status of model call response */
    status?: number;
    /** Error received in response from a model call */
    error?: string;
    /** Feature flags enabled at time of this call */
    features?: Record<string, string>;
    /** Client environment metadata */
    client?: ClientInfo_2;
    /** Restricted content: model response choices */
    choice?: ModelChoice[];
    /** Restricted content: stringified prompt messages */
    messages?: string;
}

/**
 * Restricted bag-shaped telemetry event.
 * Maps to: copilot_cli.v0.RestrictedTelemetry (restricted_telemetry.proto)
 */
declare interface HydroRestrictedTelemetry {
    /** Event type/kind */
    kind: string;
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** Reference to the model call that produced this event */
    model_call_id?: string;
    /** Joined properties including restricted props */
    properties: Record<string, string | undefined>;
    /** Numeric metrics */
    metrics: Record<string, number | undefined>;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** Feature flags enabled for this session */
    features?: Record<string, string>;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID for user-level attribution */
    copilot_tracking_id?: string;
    /** Client environment metadata */
    client?: ClientInfo_2;
}

/**
 * Restricted tool call telemetry event with sensitive content.
 * Maps to: copilot_cli.v0.RestrictedToolCall (restricted_tool_call.proto)
 */
declare interface HydroRestrictedToolCall {
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** Links to the model call that requested this tool */
    model_call_id?: string;
    /** Tool name (hashed for privacy unless safe_for_telemetry) */
    tool_name: string;
    /** Tool call ID from the model response */
    tool_call_id: string;
    /** Result of tool execution */
    result_type: ToolResultType;
    /** Execution duration in milliseconds */
    duration_ms: number;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID */
    copilot_tracking_id?: string;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** Feature flags enabled at time of this call */
    features?: Record<string, string>;
    /** Client environment metadata */
    client?: ClientInfo_2;
    /** Restricted content: tool call arguments */
    arguments?: string;
    /** Restricted content: tool call response content */
    content?: string;
    /** Restricted content: tool call error */
    error?: string;
}

/**
 * Generic bag-shaped telemetry event.
 * Maps to: copilot_cli.v0.Telemetry (telemetry.proto)
 */
declare interface HydroTelemetry {
    /** Event type/kind (e.g., "get_completion_with_tools_turn", "tool_call_executed") */
    kind: string;
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** Reference to the model call that produced this event */
    model_call_id?: string;
    /** Non-restricted properties (key-value pairs) */
    properties: Record<string, string | undefined>;
    /** Numeric metrics */
    metrics: Record<string, number | undefined>;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** Feature flags enabled for this session */
    features?: Record<string, string>;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID for user-level attribution */
    copilot_tracking_id?: string;
    /** Client environment metadata */
    client?: ClientInfo_2;
}

/**
 * Structured tool call telemetry event.
 * Maps to: copilot_cli.v0.ToolCall (tool_call.proto)
 */
declare interface HydroToolCall {
    /** Timestamp when the event was created (ISO 8601 format) */
    created_at?: string;
    /** Links to the model call that requested this tool */
    model_call_id?: string;
    /** Tool name (hashed for privacy unless safe_for_telemetry) */
    tool_name: string;
    /** Tool call ID from the model response */
    tool_call_id: string;
    /** Result of tool execution */
    result_type: ToolResultType;
    /** Execution duration in milliseconds */
    duration_ms: number;
    /** Session identifier (auto-filled by SessionTelemetry) */
    session_id?: string;
    /** Copilot tracking ID */
    copilot_tracking_id?: string;
    /** Experiment assignment context */
    exp_assignment_context?: string;
    /** Feature flags enabled at time of this call */
    features?: Record<string, string>;
    /** Client environment metadata */
    client?: ClientInfo_2;
}

export declare type ImageContent = z_2.infer<typeof ImageContentSchema>;

/**
 * Image content block with base64-encoded data
 */
declare const ImageContentSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"image">;
    data: z_2.ZodString;
    mimeType: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    data: string;
    type: "image";
    mimeType: string;
}, {
    data: string;
    type: "image";
    mimeType: string;
}>;

/**
 * This event is temporary until we extract vision support from being internal to getCompletionWithTools.
 */
declare type ImageProcessingEvent = {
    kind: "image_processing";
    turn: number;
    imageProcessingMetrics: ImageProcessingMetrics;
};

declare type ImageProcessingMetrics = ({
    imagesExtractedCount: number;
    base64ImagesCount: number;
    imagesRemovedDueToSize: number;
    imagesRemovedDueToDimensions: number;
    imagesResized: number;
    imagesResolvedFromGitHubMCPCount: number;
    allImagesSendToLlm?: number;
} & Record<string, number>) | Record<string, never>;

/**
 * This event is temporary until we extract vision support from being internal to getCompletionWithTools.
 */
declare type ImageRemovalEvent = {
    kind: "images_removed";
    turn: number;
    largeImagesRemoved?: number;
    imagesRemoved: number;
};

/** Infer the function signature for a client→server method. */
declare type InferMethod<M> = M extends {
    params: z.ZodTypeAny;
    result: z.ZodTypeAny;
} ? (params: z.infer<M["params"]>) => z.infer<M["result"]> | Promise<z.infer<M["result"]>> : M extends {
    params: z.ZodTypeAny;
} ? (params: z.infer<M["params"]>) => unknown : M extends {
    result: z.ZodTypeAny;
} ? () => z.infer<M["result"]> | Promise<z.infer<M["result"]>> : never;

/**
 * Configuration for infinite sessions with automatic context compaction and workspace persistence.
 * When enabled, sessions automatically manage context window limits through background compaction
 * and persist state to a workspace directory.
 */
declare interface InfiniteSessionConfig {
    /**
     * Whether infinite sessions are enabled.
     * @default true
     */
    enabled?: boolean;
    /**
     * Context utilization threshold (0.0-1.0) at which background compaction starts.
     * Compaction runs asynchronously, allowing the session to continue processing.
     * @default 0.80
     */
    backgroundCompactionThreshold?: number;
    /**
     * Context utilization threshold (0.0-1.0) at which the session blocks until compaction completes.
     * This prevents context overflow when compaction hasn't finished in time.
     * @default 0.95
     */
    bufferExhaustionThreshold?: number;
}

/**
 * A message to be injected into the conversation history after tool execution.
 * Used by tools like skills that need to add content to the conversation.
 */
declare type InjectedUserMessage = {
    /**
     * The content to inject as a user message.
     */
    content: string;
    /**
     * A source identifier for tracking where this message came from.
     * Used for filtering in the timeline display.
     * Example: "skill-pdf", "skill-code-reviewer"
     */
    source: string;
};

/**
 * In-memory transport that directly calls server methods without serialization.
 * This transport is used for servers that run in the same process as the client.
 */
declare class InMemoryClientTransport implements Transport {
    private server;
    onmessage?: (message: JSONRPCMessage) => void;
    onerror?: (error: Error) => void;
    onclose?: () => void;
    private serverTransport?;
    private closed;
    constructor(server: McpServer);
    /**
     * Set up bidirectional connection between client and server transports
     */
    start(): Promise<void>;
    send(message: JSONRPCMessage): Promise<void>;
    receive(message: JSONRPCMessage): void;
    close(): Promise<void>;
}

declare interface InMemoryTransportConfig {
    type: "memory";
    server: McpServer;
}

declare class InProcMCPTransport extends MCPTransport<ClientInfo> {
    private toolIdToClientInfo;
    constructor(settings: RuntimeSettings, logger: RunnerLogger, cacheProviderTools?: boolean);
    /**
     * Hook called when a provider (MCP client) is being refreshed.
     * Clears the tool ID mappings for this client.
     */
    protected onProviderRefresh(clientInfo: ClientInfo): Promise<void>;
    protected doInvokeTool(toolId: string, toolParams: any, toolCallId?: string, retryCount?: number): Promise<InvokeToolResponseData>;
    protected getProviderCacheKey(provider: ClientInfo): string;
    protected loadToolsFromProvider(clientInfo: ClientInfo): Promise<Record<string, BasicToolConfig>>;
    private static getToolIdFromClientAndToolName;
    private static getToolNameFromIdAndClientName;
}

/**
 * An installed plugin entry in user/project config.
 */
declare interface InstalledPlugin {
    /** Plugin name */
    name: string;
    /** Marketplace the plugin came from (empty string for direct repo installs) */
    marketplace: string;
    /** Version installed (if available) */
    version?: string;
    /** Installation timestamp */
    installed_at: string;
    /** Whether the plugin is currently enabled */
    enabled: boolean;
    /** Path where the plugin is cached locally */
    cache_path?: string;
    /** Source for direct repo installs (when marketplace is empty) */
    source?: MarketplaceSource;
}

declare type InstructionSource = {
    /** Unique identifier for this source (used for toggling) */
    id: string;
    /** Human-readable label */
    label: string;
    /** File path relative to repo or absolute for home */
    sourcePath: string;
    /** Raw content of the instruction file */
    content: string;
    /** Category of instruction source — used for merge logic */
    type: InstructionSourceType;
    /** Where this source lives — used for UI grouping */
    location: InstructionSourceLocation;
};

/** Where an instruction source lives — used for UI grouping */
declare enum InstructionSourceLocation {
    User = "user",
    Repository = "repository",
    WorkingDirectory = "working-directory"
}

/** Represents an individual instruction source before merging */
/** Category of instruction source — used for merge logic */
declare enum InstructionSourceType {
    Home = "home",
    Repo = "repo",
    Model = "model",
    VSCode = "vscode",
    NestedAgents = "nested-agents",
    ChildInstructions = "child-instructions"
}

export declare const INTEGRATION_ID: string;

export declare namespace internal {
    export {
        scoreSessionRelevance,
        HandoffStep,
        HandoffProgress,
        PruneResult,
        RelevanceScore,
        ScoredSessionMetadata,
        RELEVANCE_LABELS,
        LocalSessionManager
    }
}

/**
 * Information about a skill that was invoked during the session.
 * Used to preserve skill context across compaction.
 */
export declare interface InvokedSkillInfo {
    /** The skill name */
    name: string;
    /** Path to the SKILL.md file */
    path: string;
    /** The full content of the skill file */
    content: string;
    /** Turn number when the skill was invoked */
    invokedAtTurn: number;
}

declare type InvokeToolResponseData = {
    isToolError: boolean;
    content: any[];
    structuredContent?: unknown;
};

declare interface IOnRequestErrorProcessor extends IToJson {
    /**
     * Called before an error is rethrown by the client. The processor may modify
     * the error in place.
     */
    preErrorThrow(error: unknown): Promise<void>;
    /**
     * Called when a request to the model fails. The processor should not modify
     * the error.
     */
    onRequestError(context: OnRequestErrorContext): Promise<OnRequestErrorResult | void>;
}

declare interface IOnStreamingChunkProcessor extends IToJson {
    /**
     * Called when a streaming chunk is received.
     */
    onStreamingChunk(context: StreamingChunkContext): void;
}

declare interface IPostRequestProcessor extends IToJson {
    /**
     * Called after a successful request to the model, before the model_call_success event.
     * Processors can inspect the response and throw an error to trigger retry logic
     * via onRequestError processors.
     *
     * - Any {@link Event}s emitted by this method will be re-emitted by the completion with tools call.
     * - To trigger a retry, throw an error that an {@link IOnRequestErrorProcessor} can handle.
     */
    postRequest(context: PostRequestContext): AsyncGenerator<Event_2, PostRequestResult | void>;
}

declare interface IPostToolExecutionProcessor extends IToJson {
    /**
     * Called after a tool has been executed. The processor should not
     * modify the tool result.
     */
    postToolExecution(context: PostToolExecutionContext): Promise<void>;
}

declare interface IPreRequestProcessor extends IToJson {
    /**
     * Called before a request (including retries of requests) is made to the model.
     *
     * - Any {@link Event}s emitted by this method will be re-emitted by the completion with tools call.
     */
    preRequest(context: PreRequestContext): AsyncGenerator<Event_2>;
}

declare interface IPreToolsExecutionProcessor extends IToJson {
    /**
     * Called before any tool calls are executed.
     */
    preToolsExecution(context: PreToolsExecutionContext): Promise<PreToolsExecutionResult>;
}

/**
 * The complete settings type that represents all possible configuration options.
 */
declare interface IRuntimeSettings {
    version: string;
    clientName: string;
    github: {
        serverUrl: string;
        uploadsUrl: string;
        downloadsUrl: string;
        secretScanningUrl: string;
        host: string;
        hostProtocol: string;
        token: string;
        user: {
            name: string;
            email: string;
            actorId?: number;
            actorLogin?: string;
        };
        owner: {
            id: number;
            name: string;
        };
        repo: {
            id: number;
            name: string;
            branch: string;
            commit: string;
            readWrite: boolean;
        };
        pr: {
            commitCount?: number;
        };
    };
    problem: {
        statement: string;
        contentFilterMode?: ContentFilterMode;
        action: AgentAction;
        customAgentName?: string;
    };
    service: {
        instance: {
            id: string;
        };
        /**
         * - Options beyond `model` are currently only respected
         * when going through `RuntimeHarness` methods.
         * - The value of this {@link ClientOptions.model} is NOT
         * the name of a model, it is AgentName:ModelName.
         */
        agent: ClientOptions;
        /**
         * Settings for tools that are used by the agent. Refer to the
         * source/documentation for each tool for their specific settings.
         */
        tools: {
            [toolName: string]: {
                [key: string]: unknown;
            };
        };
        callback: {
            url: string;
        };
    };
    api: {
        aipSweAgent: {
            token: string;
        };
        anthropic: {
            key: string;
            baseUrl: string;
        };
        openai: {
            baseUrl: string;
            apiKey: string;
            azureKeyVaultUri: string;
            azureSecretName: string;
            azure: {
                url: string;
                apiVersion: string;
                /** Bearer token for Azure AD authentication. When set, used instead of API key or managed identity. */
                bearerToken?: string;
            };
        };
        copilot: {
            url: string;
            integrationId: string;
            hmacKey: string;
            azureKeyVaultUri: string;
            token: string;
            useSessions: boolean;
            useAsyncSessions: boolean;
            sessionId: string;
            previousSessionIds: string[];
            /**
             * The W3C Trace Context traceparent header value for distributed tracing.
             * Format: {version}-{trace-id}-{parent-id}-{trace-flags}
             * Example: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01
             */
            traceParent: string;
        };
        github: {
            /**
             * The GITHUB_PERSONAL_ACCESS_TOKEN that is passed to `github-mcp-server` when it is
             * started. (Currently only supported in the `cpd` entry point.)
             */
            mcpServerToken: string;
        };
    };
    blackbird: {
        mode: "initial-search" | "tool";
        backfillScoreThreshold?: number;
        repoNwo?: string;
        /**
         * The auth object contains the credentials for Blackbird's Metis API.
         * - modelBasedRetrievalToken: Token for model-based retrieval.
         * - metisApiKey: API key for Metis.
         */
        auth: {
            modelBasedRetrievalToken: string;
            metisApiKey: string;
        };
    };
    swebench_base_commit?: string;
    trajectory: {
        outputFile: string;
    };
    logs: {
        eventsLogDir: string;
    };
    job: {
        nonce?: string;
        eventType?: string;
    };
    onlineEvaluation: {
        disableOnlineEvaluation?: boolean;
        enableOnlineEvaluationOutputFile?: boolean;
    };
    /**
     * Test-only: Pre-formatted memories to inject into the agent's context.
     * Used by eval tests to seed memories without file I/O or cloud API calls.
     * When set, getMemoriesPrompt() returns this value directly.
     */
    testInjectedMemories?: string;
    tools: {
        bash: {
            /**
             * The default timeout for bash commands in seconds. If undefined, a default of 120 seconds (2 minutes) is used.
             */
            defaultTimeout?: number;
        };
        /**
         * Settings shared by all validation tools.
         */
        validation?: {
            /**
             * The shared timeout budget for all the validation tools in seconds. If undefined, a default of 180 seconds is used.
             * This timeout is shared across all validation tools, and once the total time spent exceeds this budget, no further validation tools will be run.
             * A validation tool will be cancelled if it is in progress when the budget is exceeded.
             */
            timeout?: number;
            /**
             * The timeout for the dependabot checker in seconds. Default is 240 seconds.
             */
            dependabotTimeout?: number;
            /**
             * Settings for the CodeQL security checker tool.
             * When enabled is false, the tool will not run even if feature flags are enabled.
             * When enabled is true or undefined, the tool's availability will be determined by its feature flags.
             */
            codeql?: {
                enabled?: boolean;
            };
            /**
             * Settings for the code review (Autofind) tool.
             * When enabled is false, the tool will not run even if feature flags are enabled.
             * When enabled is true or undefined, the tool's availability will be determined by its feature flags.
             */
            codeReview?: {
                enabled?: boolean;
            };
            /**
             * Settings for the dependency/advisory checker tool and the corresponding post-commit hook.
             * When enabled is false, the tool will not run even if feature flags are enabled.
             * When enabled is true or undefined, the tool's availability will be determined by its feature flags.
             * The validation settings for the dependency/advisory checker tool control the availability of
             * DependaBot in the agent's toolbox and with respect its use post-commit.
             */
            advisory?: {
                enabled?: boolean;
            };
            /**
             * Settings for the secret scanning hook.
             * When enabled is false, the hook will not run even if feature flags are enabled.
             * When enabled is true or undefined, the hook's availability will be determined by its feature flags.
             * Note: the secret scanning hook is a pre-commit and pre-PR description hook that scans for secrets
             * before code is committed, and before PR descriptions are created/changed.
             */
            secretScanning?: {
                enabled?: boolean;
            };
        };
        /**
         * Settings for handling large tool outputs.
         */
        largeOutput?: {
            /**
             * Maximum size in bytes before output is written to a temp file. Default is 50KB.
             */
            maxSizeBytes?: number;
            /**
             * Directory to write temp files to. Default is os.tmpdir().
             */
            outputDir?: string;
        };
    };
    /**
     * The set of feature flags passed to the agent runtime process by sweagentd.
     *
     * Only flags listed in internal/launcher/runtime_feature_flags.go are passed
     * to the runtime.
     *
     * To add a new flag:
     * - Define it in accordance with the feature flag docs: https://thehub.github.com/epd/engineering/products-and-services/dotcom/features/feature-flags/
     * - Add it to runtime_feature_flags.go
     * - Check whether it exists in the following object.
     *
     * Read a feature flag value with: @see isFeatureFlagEnabled
     *
     *    if (isFeatureFlagEnabled(settings, 'copilot_swe_agent_flag_name')) {
     *    }
     *
     * Report feature flag values in telemetry with: @see featureFlagsAsString in
     * a property named @see FEATURE_FLAGS_TELEMETRY_PROPERTY_NAME.
     *
     * NOTE: feature flag names may be visible to the user in logs or other output.
     */
    featureFlags: {
        [key: string]: boolean;
    };
    /**
     * EXP experiment configuration
     */
    experiments: {
        [key: string]: string;
    };
    /**
     * How many ms the runtime/the thing hosting the runtime has available to run
     * before it is considered to have timed out.
     */
    timeoutMs: number;
    /**
     * The time when the runtime/the thing hosting the runtime started, in ms since epoch.
     * May not be 100% accurate. Not typically set by hand.
     */
    startTimeMs: number;
    /**
     * Custom configuration directory for the session.
     * When set, overrides the default Copilot config directory (~/.copilot or $XDG_CONFIG_HOME/.copilot).
     */
    configDir: string;
    /**
     * LSP-specific settings.
     */
    lsp?: {
        clientName: string;
    };
}

/**
 * Returns true if the DEBUG or COPILOT_AGENT_DEBUG environment variable is set to 1 or true (case-insensitive).
 * If additionalVariables are provided, they are also checked.
 * @param additionalVariables Additional environment variables to check for debug logging.
 */
export declare function isDebugEnvironment(...additionalVariables: string[]): boolean;

/**
 * Something which must have an implementation of `toJSON()`. This can be used
 * for classes whose instances will likely be used with `JSON.stringify()` to avoid
 * any issues with stringification such as circular references or non-enumerable properties.
 *
 * More information on `toJSON()`: https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/JSON/stringify#:~:text=If%20the%20value%20has%20a%20toJSON()%20method%2C%20it%27s%20responsible%20to%20define%20what%20data%20will%20be%20serialized.
 */
declare interface IToJson {
    toJSON(): string;
}

declare type LargeOutputOptions = {
    maxOutputSizeBytes: number;
    outputDir?: string;
};

/**
 * Configuration for handling large tool outputs.
 */
declare interface LargeToolOutputConfig {
    /**
     * Whether large output handling is enabled. Default is true.
     */
    enabled?: boolean;
    /**
     * Maximum size in bytes before output is written to a temp file. Default is 50KB.
     */
    maxSizeBytes?: number;
    /**
     * Directory to write temp files to. Default is os.tmpdir().
     */
    outputDir?: string;
}

/**
 * Session class with event sourcing
 */
export declare class LocalSession extends Session<LocalSessionMetadata> {
    private callback;
    private isProcessing;
    private itemQueue;
    private premiumRequestProcessor;
    private immediatePromptProcessor;
    private notificationBuffer;
    private compactionProcessor;
    private abortController?;
    private queuedCommandHandler;
    private activeSubAgents;
    mcpHostCache: McpHostCache;
    private modelListCache;
    private warnedUnknownTools;
    private sessionWorkspace;
    private workspaceEnabled;
    private lastTodoContent;
    private lastPlanUpdateTurn;
    private currentTurn;
    private static readonly PLAN_REMINDER_TURN_THRESHOLD;
    private cachedRepoMemories;
    private compactionCancelled;
    private manualCompactionAbortController;
    /**
     * Creates a new Session instance.
     *
     * In practice, use SessionManager.createSession() to create sessions and SessionManager.getSession() / SessionManager.getLastSession() to retrieve existing sessions.
     *
     * @param options - Configuration options for the session including model provider, tools, hooks, environment settings, and metadata (sessionId, startTime, modifiedTime). If metadata is not provided, new values are generated.
     */
    constructor(options?: SessionOptions);
    /**
     * Normalize infiniteSessions config from boolean or object to a full config object.
     */
    private normalizeInfiniteSessionsConfig;
    /**
     * Update workspace summary from user message content.
     */
    private updateWorkspaceSummary;
    /**
     * Initialize workspace - load existing or create new.
     * Workspaces are always created when infinite sessions are enabled.
     */
    private initializeWorkspace;
    /**
     * Update the workspace context based on current workspace state.
     * This context is used in system prompts to inform the agent about workspace files.
     */
    private updateWorkspaceContext;
    /**
     * Updates session options after creation.
     * This method allows selectively updating configuration options without recreating the session.
     * Only the provided options will be updated; omitted options remain unchanged.
     *
     * @param options - Partial session options to update
     *
     * @example
     * ```typescript
     * // Update multiple options at once
     * session.updateOptions({
     *   logger: fileLogger,
     *   mcpServers: mcpConfig,
     *   customAgents: loadedAgents
     * });
     *
     * // Or use convenience methods for single updates
     * session.setAuthInfo(newAuthInfo); // Preferred for auth
     * session.setSelectedModel(newModel); // Preferred for model
     * ```
     */
    updateOptions(options: Partial<UpdatableSessionOptions>): void;
    getMetadata(): LocalSessionMetadata;
    /**
     * Get the current workspace, if any.
     */
    getWorkspace(): Workspace | null;
    /**
     * Check if workspace features are enabled.
     */
    isWorkspaceEnabled(): boolean;
    /**
     * Get the workspace path for this session.
     * Returns null if workspace features are not enabled.
     * Returns the path even if workspace.yaml doesn't exist yet (for prompt context).
     */
    getWorkspacePath(): string | null;
    /**
     * Get the number of checkpoints (compaction summaries) in the workspace.
     */
    getCheckpointCount(): number;
    /**
     * Update the session's summary (AI-generated name).
     * Updates both in-memory workspace and persists to disk.
     * Will not overwrite a manually set name.
     */
    updateSessionSummary(summary: string): Promise<void>;
    /**
     * Rename the session (set custom name).
     * Updates both in-memory workspace and persists to disk.
     * Emits session.title_changed event so UI can update.
     */
    renameSession(name: string): Promise<void>;
    /**
     * List checkpoints with their titles for context injection.
     */
    listCheckpointTitles(): Promise<{
        number: number;
        title: string;
        filename: string;
    }[]>;
    /**
     * Check if a plan.md file exists in the workspace.
     */
    hasPlan(): boolean;
    /**
     * Get plan.md content for post-compaction message.
     * Returns null if workspace is not enabled or plan doesn't exist.
     */
    private getPlanContentForCompaction;
    /**
     * Update the last todo content (called when update_todo tool is used).
     * This content will be included in post-compaction messages.
     */
    setLastTodoContent(content: string | null): void;
    /**
     * Get the last todo content.
     */
    getLastTodoContent(): string | null;
    /**
     * Check if a plan update reminder should be shown.
     * Returns true if:
     * - Workspace is enabled
     * - Plan.md hasn't been updated in the last N turns
     * - A plan.md file exists (we only remind to update, not create)
     */
    shouldShowPlanReminder(): boolean;
    /**
     * Get the plan reminder message to inject into user prompts.
     * Returns null if no reminder should be shown.
     */
    getPlanReminderMessage(): string | null;
    /**
     * Mark plan as recently updated (resets the reminder timer).
     * Called when plan.md is detected to be written.
     */
    markPlanUpdated(): void;
    /**
     * Handle a subagent_session_boundary event by emitting the appropriate
     * subagent lifecycle session event (started/completed/failed).
     */
    private handleSubagentBoundary;
    /**
     * Emit telemetry when agent writes to a workspace file (plan.md or files/).
     */
    private emitWorkspaceFileTelemetry;
    /**
     * Emit telemetry when agent reads a workspace file (plan.md, checkpoints, or files/).
     */
    private emitWorkspaceFileReadTelemetry;
    /**
     * Ensure workspace exists for this session.
     * Creates workspace.yaml and directory structure if needed.
     */
    ensureWorkspace(context?: WorkspaceContext): Promise<Workspace>;
    /**
     * Persist a compaction summary as a checkpoint.
     * Called automatically when compaction completes.
     * Returns the checkpoint number and path after the file is created.
     */
    private persistCompactionCheckpoint;
    /**
     * Truncate workspace checkpoints to align with the current session history.
     * Used after rollback to remove compaction checkpoints created after the rollback point.
     */
    truncateWorkspaceCheckpoints(keepCount: number): Promise<void>;
    /**
     * Sends a message to the session and executes the agentic loop.
     * Messages can be queued or sent immediately during an ongoing turn.
     *
     * @param options - Send options including prompt, attachments, and mode
     * @param options.prompt - The prompt text to send
     * @param options.attachments - Optional file/directory attachments
     * @param options.mode - "enqueue" (default) adds to queue and processes when ready, "immediate" injects during current turn
     * @param options.abortController - Optional AbortController to abort the send operation
     * @returns A Promise that resolves when the message has been queued or processed
     *
     * @example
     * ```typescript
     * // Send a message (default enqueue mode)
     * session.send({
     *   prompt: "What files are in this directory?",
     *   attachments: [{ type: "directory", path: "/path/to/dir" }]
     * });
     *
     * // Send immediate message during processing
     * session.send({
     *   prompt: "Continue with that approach",
     *   mode: "immediate"
     * });
     * ```
     */
    send(options: SendOptions): Promise<void>;
    /**
     * Send a system notification to the agent.
     * Notifications are buffered and batched — they'll be delivered as a single
     * `<system_notification>` block either after a 5s batch window, when a user
     * message arrives, or at the next preRequest.
     *
     * System notifications are hidden from the timeline, non-billable, and excluded
     * from session snapshots.
     */
    sendSystemNotification(message: string): void;
    /**
     * Flush buffered system notifications and send them to the agent.
     * Called by the batch timer when no user messages arrive to piggyback on.
     */
    private flushAndSendNotifications;
    /**
     * Core logic for adding an item to the queue.
     *
     * @param item - The queued item (either a command or message)
     * @param prepend - If true, adds to the front of the queue (for priority messages)
     */
    private addItemToQueue;
    /**
     * Enqueue any item (command or message) to be processed after the current agentic work completes.
     * Items are processed in FIFO order.
     *
     * If the session is not currently processing, this will also kick off queue processing.
     * This ensures that items transferred from another session (e.g., after /clear)
     * actually get executed even if there are no messages to trigger processing.
     *
     * @param item - The queued item (either a command or message)
     */
    enqueueItem(item: QueuedItem): void;
    /**
     * Enqueue a slash command to be executed after the current agentic work completes.
     * Commands are processed in FIFO order alongside user messages.
     *
     * If the session is not currently processing, this will also kick off queue processing.
     * This ensures that commands transferred from another session (e.g., after /clear)
     * actually get executed even if there are no messages to trigger processing.
     *
     * @param command - The full command string including the slash, e.g., "/compact" or "/model gpt-4"
     */
    enqueueCommand(command: string): void;
    /**
     * Enqueue a user message item to be processed later.
     * This is a utility for creating properly typed message queue items.
     * Uses addItemToQueue internally for consistent mode normalization.
     *
     * @param options - The send options for the message
     * @param prepend - If true, adds to the front of the queue (for priority messages)
     */
    private enqueueUserMessage;
    /**
     * Set the handler for executing queued slash commands.
     * This should be called by the CLI to register its slash command execution logic.
     * Pass undefined to clear the handler (e.g., during cleanup).
     */
    setQueuedCommandHandler(handler: QueuedCommandHandler | undefined): void;
    /**
     * Process the item queue, handling both messages and commands in FIFO order.
     */
    private processQueue;
    abort(): Promise<void>;
    /**
     * Check if the session is currently in a state where it can be aborted.
     * Returns true if there's an active abort controller that hasn't been aborted yet.
     * This is important for queued operations where the CLI may not have direct access
     * to the abort controller (e.g., when messages are processed from the queue).
     */
    isAbortable(): boolean;
    /**
     * TODO(queueing-improvements)
     * If we support steering or queuing for remote sessions, these will need
     * to become async, but since we don't right now, it is MUCH easier for the UI
     * to have them be synchronous.
     */
    getPendingSteeringMessagesDisplayPrompt(): ReadonlyArray<string>;
    getPendingQueuedMessagesDisplayPrompt(): ReadonlyArray<string>;
    /**
     * Get pending queued items for UI display.
     * Returns both messages and commands with their display text.
     */
    getPendingQueuedItems(): ReadonlyArray<PendingQueuedItem>;
    /**
     * @deprecated Use getPendingQueuedItems() instead for proper display of mixed queue items.
     */
    getPendingQueuedMessages(): ReadonlyArray<string>;
    /**
     * Clear all pending steering and queued items (messages and commands).
     * Used internally when the agentic loop is aborted (e.g., user rejected a tool permission).
     */
    clearPendingItems(): void;
    /**
     * @deprecated Use clearPendingItems() instead.
     */
    clearPendingMessages(): void;
    /**
     * Compacts the conversation history into a single summary message.
     * This method is used by the /compact slash command for manual compaction.
     * Uses the same system message and tools as the core agent loop for consistency.
     *
     * @returns Promise that resolves with compaction results
     * @throws Error if compaction fails or prerequisites aren't met
     */
    compactHistory(): Promise<CompactionResult>;
    /**
     * Process queued items (messages and commands) through the agentic loop.
     * Used by send() and after manual compaction completes.
     * No-op if already processing or queue is empty.
     */
    private processQueuedItems;
    /**
     * Handle background compaction completion callback.
     * This is called immediately when background compaction finishes, allowing the session
     * to emit events and update state without waiting for the next preRequest call.
     */
    private handleCompactionComplete;
    /**
     * Cancels any in-progress background compaction.
     *
     * This should be called when the session state is being rolled back (e.g., Esc Esc),
     * to prevent the compaction result from being applied after the rollback.
     */
    cancelBackgroundCompaction(): void;
    abortManualCompaction(): boolean;
    /**
     * Initialize MCP host if configured
     */
    private initializeMcpHost;
    /**
     * Get connected IDE info if available.
     * Returns undefined if no IDE is connected or if the MCP host is not a CliMcpHost.
     */
    private getConnectedIdeInfo;
    /**
     * Initializes the session and validates tool filter configuration.
     * This method should be called after the session is fully configured (auth, model, MCP servers)
     * but before the first message is sent. It eagerly builds and caches the tool definitions and system message
     * so they are available for features like /context that need them before the first message.
     * It will also emit warnings for any unknown tool names specified in availableTools or excludedTools.
     *
     * @returns Promise that resolves when initialization and validation is complete
     */
    initializeAndValidateTools(): Promise<void>;
    /**
     * Shared method to build settings and initialize tools.
     * Used by both initializeAndValidateTools() and runAgenticLoop().
     *
     * @param problemStatement - Optional problem statement for settings (used by runAgenticLoop)
     * @returns Settings and tools, or undefined if initialization failed
     */
    private buildSettingsAndTools;
    private isToolEnabled;
    /**
     * Validates tool filter configuration and emits info about disabled tools and warnings for unknown tool names.
     */
    private validateToolFilters;
    /**
     * Filters tools based on the selected custom agent, if any.
     *
     * @param allTools - All available tools
     * @returns Filtered tools based on selected custom agent restrictions
     */
    private filterToolsForSelectedAgent;
    private invokeCallbacks;
    private getModelList;
    /**
     * Creates a client instance with current session configuration.
     * Extracted from runAgenticLoop to allow reuse for standalone LLM calls.
     *
     * @returns Promise that resolves to a configured client and settings
     * @throws Error if session was not created with authentication info or custom provider
     */
    private getClient;
    /**
     * Generates a summarized version of the conversation context suitable for delegation.
     * Uses an LLM to create a concise summary of the existing conversation that fits
     * within size constraints (20k characters).
     *
     * @returns Promise that resolves to a markdown summary of the session context
     * @throws Error if session was not created with authentication info or if summarization fails
     */
    getContextSummary(): Promise<string>;
    /**
     * Executes the full agentic loop for a given prompt.
     * This method orchestrates the complete AI agent workflow including:
     * - Running hooks (userPromptSubmitted, sessionStart, preToolUse, postToolUse, sessionEnd)
     * - Building and sending the prompt to the language model
     * - Processing model responses and tool calls
     * - Executing tools and feeding results back to the model
     * - Emitting events throughout the process
     *
     * This is the core method that powers the `send()` functionality.
     * Most users should call `send()` instead, which handles queuing and mode selection.
     *
     * @param prompt - The user's prompt/instruction text
     * @param attachments - Optional array of file or directory attachments to include with the prompt
     * @returns A Promise that resolves when the agent loop completes
     * @throws Error if the session was not created with authentication info/custom provider or model
     */
    private runAgenticLoop;
}

/**
 * SessionManager subclass that persists sessions to JSONL files.
 * Uses SessionEventState for all file system operations.
 */
declare class LocalSessionManager implements SessionManager<LocalSessionMetadata, LocalSession>, Disposable_2 {
    private sessionWriters;
    private copilotVersion;
    private flushDebounceMs;
    private settings?;
    readonly sessionTelemetry: SessionTelemetry;
    constructor({ version, flushDebounceMs, settings, telemetryService }: LocalSessionManagerOptions);
    dispose(): void;
    /**
     * Get effective settings, preferring options.configDir over this.settings
     */
    private getEffectiveSettings;
    /**
     * Create a new file-backed session
     */
    createSession(options?: SessionOptions, emitStart?: boolean): Promise<LocalSession>;
    private loadHooks;
    /**
     * Get existing session by ID
     * ALWAYS loads from disk to ensure freshness
     * Supports both JSONL and legacy JSON formats
     *
     * @throws Error if the session exists but cannot be parsed (e.g., unknown event types, corrupt data)
     * @returns The session, or undefined if the session does not exist
     */
    getSession(options: SessionOptions & {
        sessionId: string;
    }, resume?: boolean): Promise<LocalSession | undefined>;
    /**
     * Get the most recently updated session
     */
    getLastSession(options?: Omit<SessionOptions, "sessionId">): Promise<LocalSession | undefined>;
    /**
     * Get the ID of the most recently updated session
     */
    getLastSessionId(): Promise<string | undefined>;
    saveSession(session: Session): Promise<void>;
    private loadSession;
    /**
     * Load a legacy JSON session and convert it to LocalSession
     * This creates a session.import_legacy event to preserve the legacy data
     */
    private loadLegacySession;
    /**
     * List all sessions by reading from disk
     */
    listSessions(): Promise<LocalSessionMetadata[]>;
    /**
     * List sessions sorted by relevance to the current working directory context.
     * Sessions matching the current repo+branch appear first, followed by same repo,
     * then same gitRoot or cwd, then all others sorted by time.
     *
     * @param currentContext The current working directory context to compare against
     */
    listSessionsSortedByRelevance(currentContext?: WorkingDirectoryContext): Promise<LocalSessionMetadata[]>;
    /**
     * List sessions with their relevance scores for grouping in the UI.
     * Sessions are sorted by score (descending), then by modifiedTime (descending).
     *
     * @param currentContext The current working directory context to compare against
     */
    listSessionsWithScores(currentContext?: WorkingDirectoryContext): Promise<ScoredSessionMetadata<LocalSessionMetadata>[]>;
    /**
     * Delete a session from disk
     */
    deleteSession(sessionId: string): Promise<void>;
    /**
     * Close session - flushes pending data and cleans up resources but keeps session in memory
     */
    closeSession(sessionId: string): Promise<void>;
    /**
     * Get the sessions directory path (for debugging/logging)
     */
    getSessionsDirectory(): string;
    /**
     * Get the disk size of each session's workspace directory.
     *
     * @returns Map of sessionId to size in bytes
     */
    getSessionSizes(): Promise<Map<string, number>>;
    /**
     * Delete multiple sessions, cleaning up workspace directories and legacy event files.
     *
     * @param sessionIds - Array of session IDs to delete
     * @returns Map of sessionId to bytes freed
     */
    bulkDeleteSessions(sessionIds: string[]): Promise<Map<string, number>>;
    /**
     * Find and optionally delete sessions older than a specified number of days.
     *
     * @param olderThanDays - Minimum age in days for a session to be eligible for pruning
     * @param options.dryRun - If true, only report what would be deleted (default: false)
     * @param options.includeNamed - If true, include sessions with a custom name (default: false)
     * @returns PruneResult with details about deleted/skipped sessions
     */
    pruneOldSessions(olderThanDays: number, options?: {
        dryRun?: boolean;
        includeNamed?: boolean;
        excludeSessionIds?: string[];
    }): Promise<PruneResult>;
    /**
     * Handoff a remote session to local by validating repository, checking git state,
     * and creating a local session with the remote session's events.
     * This is an async generator that yields progress updates.
     *
     * @param remoteSession - The remote session to handoff
     * @param sessionOptions - Optional session options to pass to createSession
     * @yields HandoffProgress updates for each step
     * @returns A new local session with the same events
     * @throws Error if validation fails or git operations fail
     */
    handoffSession(remoteSession: Session, sessionOptions?: SessionOptions): AsyncGenerator<HandoffProgress, LocalSession, undefined>;
    /**
     * Helper method to fetch from git remote
     */
    private gitFetch;
}

declare type LocalSessionManagerOptions = {
    version?: string;
    flushDebounceMs?: number;
    settings?: RuntimeSettings;
    telemetryService: TelemetryService;
};

export declare interface LocalSessionMetadata extends SessionMetadata {
    readonly isRemote: false;
}

/** Basic logging interface */
declare interface Logger {
    info(message: string): void;
    debug(message: string): void;
    warning(message: string): void;
    error(message: string): void;
}

export declare enum LogLevel {
    None = 0,
    Error = 1,
    Warning = 2,
    Info = 3,
    Debug = 4,
    All = 4,// Logs everything
    Default = 3
}

declare type LogLevel_2 = keyof Logger;

/** Interface for an implementer of writing log messages asynchronously. */
declare interface LogWriter {
    /** Write a log message at the given level.
     * @returns A promise that resolves when all pending writes are complete
     */
    writeLog(level: LogLevel_2, message: string): Promise<void>;
    /** @returns the current file path or other identifier if not writing to a file. */
    outputPath(): string;
}

declare type MarketplaceSource = z.infer<typeof MarketplaceSourceSchema>;

/**
 * Source for a registered marketplace (how to fetch it).
 */
declare const MarketplaceSourceSchema: z.ZodUnion<[z.ZodString, z.ZodObject<{
    source: z.ZodLiteral<"github">;
    repo: z.ZodString;
    ref: z.ZodOptional<z.ZodString>;
    path: z.ZodOptional<z.ZodString>;
}, "strip", z.ZodTypeAny, {
    repo: string;
    source: "github";
    path?: string | undefined;
    ref?: string | undefined;
}, {
    repo: string;
    source: "github";
    path?: string | undefined;
    ref?: string | undefined;
}>, z.ZodObject<{
    source: z.ZodLiteral<"url">;
    url: z.ZodString;
    ref: z.ZodOptional<z.ZodString>;
}, "strip", z.ZodTypeAny, {
    url: string;
    source: "url";
    ref?: string | undefined;
}, {
    url: string;
    source: "url";
    ref?: string | undefined;
}>, z.ZodObject<{
    source: z.ZodLiteral<"local">;
    path: z.ZodString;
}, "strip", z.ZodTypeAny, {
    source: "local";
    path: string;
}, {
    source: "local";
    path: string;
}>]>;

/**
 * Manages the lifecycle of MCP (Model Context Protocol) servers and provides access to their tools.
 */
declare class McpHost {
    protected logger: RunnerLogger;
    protected onOAuthRequired?: ((serverName: string, serverUrl: string, staticClientConfig?: {
        clientId: string;
        publicClient?: boolean;
    }) => Promise<OAuthClientProvider | undefined>) | undefined;
    protected registry: MCPRegistry;
    private processor;
    protected config: MCPServersConfig;
    private startServersPromise?;
    protected transport: InProcMCPTransport | null;
    private disabledServers;
    private progressCallback?;
    constructor(logger: RunnerLogger, mcpConfig: string | MCPServersConfig, disabledServers?: string[], envValueMode?: EnvValueMode, onOAuthRequired?: ((serverName: string, serverUrl: string, staticClientConfig?: {
        clientId: string;
        publicClient?: boolean;
    }) => Promise<OAuthClientProvider | undefined>) | undefined);
    /**
     * Called when a server sends a tools/list_changed notification
     */
    private handleToolsChanged;
    startServers(statusCallback?: ServerStatusCallback): Promise<void>;
    /**
     * Extension point for subclasses to inject default servers into the config.
     * This method is called during startServers() before processing the servers.
     * Subclasses should override this method and mutate the config parameter in place
     * to add custom server configurations.
     */
    protected injectDefaultServers(_config: MCPServersConfig): Promise<void>;
    private processServersWithExtensions;
    stopServers(): Promise<void>;
    /**
     * Gets all available tools from the MCP servers. Starts the servers with @see startServers if they have not already been started.
     * the tools returned should not be used after @see stopServers has been called.
     *
     * @param settings - The runtime settings.
     * @param logger - The logger instance.
     * @param permissions - Permissions configuration for tool access.
     * @returns A promise that resolves to an array of tools.
     */
    getTools(settings: RuntimeSettings, logger: RunnerLogger, permissions: PermissionsConfig): Promise<Tool_2[]>;
    /**
     * Gets the current MCP configuration.
     */
    getConfig(): MCPServersConfig;
    /**
     * Gets all connected MCP clients.
     * @returns A record of server names to their client instances
     */
    getClients(): Record<string, Client>;
    /**
     * Gets all servers that failed to connect.
     * @returns A record of server names to their failure information
     */
    getFailedServers(): Record<string, ServerFailureInfo>;
    /**
     * Gets the connection status of a server.
     * @param serverName - Name of the server
     * @returns The server's connection status
     */
    getServerStatus(serverName: string): ServerConnectionStatus;
    /**
     * Gets servers with pending connections.
     * @returns A record of server names to their pending connection promises
     */
    getPendingConnections(): Record<string, Promise<void>>;
    /**
     * Start a single MCP server with the given configuration
     * @param serverName - Unique name for the server
     * @param config - Server configuration
     * @returns Promise that resolves when server is started
     */
    startServer(serverName: string, config: MCPServerConfig): Promise<void>;
    /**
     * Stop a single MCP server
     * @param serverName - Name of the server to stop
     * @returns Promise that resolves when server is stopped
     */
    stopServer(serverName: string): Promise<void>;
    /**
     * Restart a server with new configuration
     * @param serverName - Name of the server to restart
     * @param config - New server configuration
     * @returns Promise that resolves when server is restarted
     */
    restartServer(serverName: string, config: MCPServerConfig): Promise<void>;
    /**
     * Check if a server is currently running
     * @param serverName - Name of the server to check
     * @returns True if server is running
     */
    isServerRunning(serverName: string): boolean;
    /**
     * Check if a server is disabled
     * @param serverName - Name of the server to check
     * @returns True if server is disabled
     */
    isServerDisabled(serverName: string): boolean;
    /**
     * Disable a server at runtime (does not persist across sessions)
     * @param serverName - Name of the server to disable
     * @returns Promise that resolves when server is disabled
     */
    disableServer(serverName: string): Promise<void>;
    /**
     * Enable a previously disabled server at runtime
     * @param serverName - Name of the server to enable
     * @returns Promise that resolves when server is enabled
     */
    enableServer(serverName: string): Promise<void>;
    /**
     * Extension point for subclasses to start a built in server that is not listed in
     * the MCP Config.
     * @param serverName - Name of the server to enable
     * @returns Promise that resolves when server handling is complete
     */
    protected startBuiltInServer(_serverName: string): Promise<void>;
    /**
     * Get the configuration for a running server
     * @param serverName - Name of the server
     * @returns Server configuration or undefined if not found
     */
    getServerConfig(serverName: string): MCPServerConfig | undefined;
    /**
     * Set the callback to be invoked when an MCP tool reports progress.
     * @param callback - The callback to invoke with (toolCallId, progressMessage)
     */
    setProgressCallback(callback: ToolProgressCallback | undefined): void;
    /**
     * Gets all server instructions from connected MCP servers.
     * Server instructions are provided by MCP servers during initialization
     * and describe how to use the server and its features.
     * @returns A record mapping server names to their instructions
     */
    getServerInstructions(): Record<string, string>;
}

/**
 * Cache that maintains a mapping of agent ID to MCP host.
 * The root agent uses an empty string as its ID.
 */
declare class McpHostCache {
    private hosts;
    private logger;
    constructor(logger: RunnerLogger);
    /**
     * Get or create an MCP host for the given agent ID.
     * Returns undefined if mcpServers is empty or undefined.
     */
    getOrCreateHost(agentId: string, mcpServers: Record<string, MCPServerConfig> | undefined): Promise<McpHost | undefined>;
    /**
     * Get an existing host for the given agent ID.
     */
    getHost(agentId: string): McpHost | undefined;
    /**
     * Stop and remove all MCP hosts.
     */
    cleanup(): Promise<void>;
    /**
     * Get the number of hosts in the cache.
     */
    size(): number;
}

declare interface MCPInMemoryServerConfig extends MCPServerConfigBase {
    type: "memory";
    serverInstance: McpServer;
}

declare interface MCPLocalServerConfig extends MCPServerConfigBase {
    type?: "local" | "stdio";
    command: string;
    args: string[];
    /**
     * An object of the environment variables to pass to the server.
     *
     * The interpretation of this object depends on the environment variable mode:
     * - In 'indirect' mode (default): Hybrid approach for backward compatibility
     *   - If value contains $ or ${...}, tries variable substitution first
     *   - If substitution succeeds (changes the value), uses the resolved value
     *   - Otherwise, treats value as the name of an env var to read from current process
     *   Example: { "FOO": "BAR" } sets FOO=process.env.BAR (legacy)
     *   Example: { "FOO": "$BAR" } sets FOO=process.env.BAR (variable expansion)
     *   Example: { "FOO": "${BAR:-default}" } sets FOO=process.env.BAR or "default"
     * - In 'direct' mode: Key is the env var name to set in the MCP server,
     *   Value is the literal value to set. Supports variable expansion:
     *   - $VAR or ${VAR}: expands to process.env.VAR
     *   - ${VAR:-default}: expands to process.env.VAR, or "default" if VAR is undefined
     *   Example: { "FOO": "bar" } sets FOO=bar
     *   Example: { "FOO": "${BAR}" } or { "FOO": "$BAR" } sets FOO=process.env.BAR
     *   Example: { "FOO": "${BAR:-fallback}" } sets FOO=process.env.BAR or "fallback"
     *   Example: { "URL": "https://${HOST}:${PORT}" } expands both variables
     *
     * Empty means no env vars passed.
     */
    env?: Record<string, string>;
    cwd?: string;
}

/**
 * A permission request for invoking an MCP tool.
 */
declare type MCPPermissionRequest = {
    readonly kind: "mcp";
    /** The name of the MCP Server being targeted e.g. "github-mcp-server" */
    readonly serverName: string;
    /** The name of the tool being targeted e.g. "list_issues" */
    readonly toolName: string;
    /** The title of the tool being targeted e.g. "List Issues" */
    readonly toolTitle: string;
    /**
     * The _hopefully_ JSON arguments that will be passed to the MCP tool.
     *
     * This should be an object, but it's not parsed before this point so we can't guarantee that.
     * */
    readonly args: unknown;
    /**
     * Whether the tool is read-only (e.g. a `view` operation) or not (e.g. an `edit` operation).
     */
    readonly readOnly: boolean;
};

declare class MCPRegistry {
    private logger;
    private clientFactory;
    private transportFactory;
    envValueMode: EnvValueMode;
    clients: Record<string, Client>;
    transports: Record<string, Transport>;
    configs: Record<string, MCPServerConfig>;
    /**
     * Server instructions provided by MCP servers during initialization.
     * These instructions describe how to use the server and its features.
     */
    serverInstructions: Record<string, string>;
    /**
     * Pending connection promises for servers that are connecting in the background.
     * Used for deferred connections where we don't want to block on server startup.
     */
    pendingConnections: Record<string, Promise<void>>;
    /**
     * Servers that failed to connect, with their error information.
     * Used to track and display connection failures to users.
     */
    failedServers: Record<string, ServerFailureInfo>;
    private toolsChangedCallback?;
    constructor(logger: RunnerLogger, clientFactory?: ClientFactory, transportFactory?: TransportFactory, envValueMode?: EnvValueMode);
    /**
     * Register a callback to be invoked when a server sends a tools/list_changed notification.
     * The callback can be sync or async.
     */
    setToolsChangedCallback(callback: (clientName: string) => void | Promise<void>): void;
    /**
     * Record a server connection failure.
     * @param serverName - Name of the server that failed
     * @param error - The error that caused the failure
     */
    recordFailure(serverName: string, error: Error): void;
    /**
     * Clear a server's failure record (e.g., when it successfully reconnects).
     * @param serverName - Name of the server
     */
    clearFailure(serverName: string): void;
    /**
     * Get the connection status of a server.
     * @param serverName - Name of the server
     * @returns The server's connection status
     */
    getServerStatus(serverName: string): ServerConnectionStatus;
    /**
     * Ensures a server is connected before invoking tools.
     * If the server has a pending connection, waits for it to complete.
     * @param serverName - Name of the server to ensure is connected
     * @throws Error if the pending connection fails
     */
    ensureConnected(serverName: string): Promise<void>;
    startLocalMcpClient(serverName: string, serverConfig: MCPLocalServerConfig): Promise<void>;
    /**
     * Start a local MCP client with deferred connection.
     * The server process is started immediately but the connection is established in the background.
     * Use this for servers where we have a static tool manifest and don't need to wait for
     * the connection to complete before using the tools list.
     *
     * The client is added to registry.clients immediately (before connection completes),
     * so it will be included when iterating over clients. Call ensureConnected() before
     * invoking any tool on this server.
     *
     * @param serverName - Name of the server
     * @param serverConfig - Server configuration
     */
    startLocalMcpClientDeferred(serverName: string, serverConfig: MCPLocalServerConfig): void;
    startHttpMcpClient(serverName: string, serverConfig: MCPRemoteServerConfig, authProvider?: OAuthClientProvider): Promise<void>;
    startSseMcpClient(serverName: string, serverConfig: MCPRemoteServerConfig, authProvider?: OAuthClientProvider): Promise<void>;
    startInMemoryMcpClient(serverName: string, serverConfig: MCPServerConfig, serverInstance: McpServer): Promise<void>;
    /**
     * Test a connection to a remote MCP server without registering it.
     * This is useful for validating server configuration before saving.
     * Throws UnauthorizedError if the server requires authentication.
     * Throws other errors if the connection fails for other reasons.
     *
     * @param serverUrl - The URL of the remote server
     * @param serverType - The type of transport ("http" or "sse")
     * @param headers - Optional headers to include in requests
     * @param authProvider - Optional OAuth provider for authenticated connections
     */
    testRemoteConnection(serverUrl: string, serverType: "http" | "sse", headers?: Record<string, string>, authProvider?: OAuthClientProvider): Promise<void>;
    private setupAndConnectClient;
    getTools(mcpServersConfig: MCPServersConfig | undefined, sessionClient?: CopilotSessionsClient): Promise<Record<string, Tool>>;
    private getServerTools;
    private logServerSuccessWithTools;
    /**
     * Gets all server instructions from connected MCP servers.
     * @returns A record mapping server names to their instructions
     */
    getServerInstructions(): Record<string, string>;
}

declare interface MCPRemoteServerConfig extends MCPServerConfigBase {
    type: "http" | "sse";
    /**
     * URL of the remote server
     * NOTE: this has to be converted to a URL object before giving to transport.
     * TransportFactory will handle this conversion.
     */
    url: string;
    /**
     * Optional. HTTP headers to include in requests to the remote server.
     * This can be used for authentication or other purposes.
     * For example, you might include an Authorization header.
     */
    headers?: Record<string, string>;
    /**
     * Optional. OAuth client ID for pre-registered (static) OAuth clients.
     * When set, dynamic client registration is skipped and this client ID is used.
     * If not set, dynamic client registration (RFC 7591) is used when OAuth is detected.
     *
     * OAuth is automatically detected by probing the server at connection time
     * (via /.well-known/oauth-protected-resource or 401 Unauthorized responses).
     */
    oauthClientId?: string;
    /**
     * Optional. Indicates whether this is a public OAuth client (no secret).
     * Defaults to true (public client).
     * When false, the client secret is retrieved from the system keychain.
     */
    oauthPublicClient?: boolean;
}

declare type MCPServerConfig = MCPLocalServerConfig | MCPRemoteServerConfig | MCPInMemoryServerConfig;

declare interface MCPServerConfigBase {
    /**
     * Optional human-readable name for this server.
     *
     * This is intended to preserve original registry/config names that may contain "/".
     * The object key used to identify the server may be adapted (e.g. "/" -> "__") for
     * internal IDs, but this value should remain exactly as provided by the user/registry.
     */
    displayName?: string;
    /**
     * List of tools to include from this server. [] means none. "*" means all.
     */
    tools: string[];
    /**
     * Indicates "remote" or "local" server type.
     * If not specified, defaults to "local".
     */
    type?: string;
    /**
     * Optional. Denotes if this is a MCP server we have defined to be used when
     * the user has not provided their own MCP server config.
     *
     * Marked optional as configs coming from users will/should not have this set. Defaults to `false`.
     */
    isDefaultServer?: boolean;
    /**
     * Optional. Either a content filter mode for all tools from this server, or a map of tool name to content filter mode for the tool with that name.
     * If not specified, defaults to "hidden_characters"
     */
    filterMapping?: Record<string, ContentFilterMode> | ContentFilterMode;
    /**
     * Optional. Timeout in milliseconds for tool calls to this server.
     * If not specified, a default is used.
     */
    timeout?: number;
    /**
     * Optional. Name of the plugin that provided this MCP server.
     * Only set for servers loaded from installed plugins.
     */
    sourcePlugin?: string;
}

declare interface MCPServersConfig {
    mcpServers: Record<string, MCPServerConfig>;
}

declare abstract class MCPTransport<ToolsProviderT = unknown> {
    protected readonly settings: RuntimeSettings;
    protected readonly logger: RunnerLogger;
    protected readonly cacheProviderTools: boolean;
    private cachedTools;
    private _progressCallback?;
    constructor(settings: RuntimeSettings, logger: RunnerLogger, cacheProviderTools: boolean);
    /**
     * Set the callback to be invoked when an MCP tool reports progress.
     */
    setProgressCallback(callback: ToolProgressCallback | undefined): void;
    protected get progressCallback(): ToolProgressCallback | undefined;
    /**
     * Refresh tools for a specific provider.
     * This completely reloads the provider's tools, clearing all cached state.
     * Called when a tools/list_changed notification is received or when tools need to be refreshed.
     */
    refreshProvider(provider: ToolsProviderT): Promise<void>;
    /**
     * Hook for subclasses to perform additional cleanup when a provider is refreshed.
     * Default implementation does nothing.
     */
    protected onProviderRefresh(_provider: ToolsProviderT): Promise<void>;
    invokeTool(toolId: string, toolParams: any, filterMode?: ContentFilterMode, toolCallId?: string): Promise<ToolResultExpanded>;
    /**
     * Allows us to directly invoke a tool, which could be one that is suppressed from the tool list.
     * - Response can be either a text or a Base64 encoded image based on mcp-client/src/MCPServer.ts implementation of the invoke-tool endpoint.
     * - If `isToolError` is true, it means the tool call failed.
     */
    protected abstract doInvokeTool(toolId: string, toolParams: any, toolCallId?: string): Promise<InvokeToolResponseData>;
    protected invokeToolResponseToToolResult(responseData: InvokeToolResponseData, filterMode: ContentFilterMode): ToolResultExpanded;
    loadTools(provider: ToolsProviderT, permissions?: PermissionsConfig): Promise<Tool_2[]>;
    protected abstract getProviderCacheKey(provider: ToolsProviderT): string;
    protected abstract loadToolsFromProvider(provider: ToolsProviderT): Promise<Record<string, BasicToolConfig>>;
}

/**
 * A permission request for storing memory.
 */
declare type MemoryPermissionRequest = {
    readonly kind: "memory";
    /** The subject of the memory being stored */
    readonly subject: string;
    /** The fact being stored */
    readonly fact: string;
    /** The source citations for the fact */
    readonly citations: string;
};

/**
 * Merges two QueryHooks objects by concatenating arrays for each hook type.
 * Does not mutate the input objects.
 */
export declare function mergeQueryHooks(existing: QueryHooks | undefined, additional: QueryHooks | undefined): QueryHooks;

/**
 * All types of message events that can be emitted by the `Client`.
 */
declare type MessageEvent_2 = {
    kind: "message";
    turn?: number;
    callId?: string;
    modelCall?: ModelCallParam;
    message: ChatCompletionMessageParam & ReasoningMessageParam;
} | AssistantMessageEvent_2 | UserMessageEvent_2 | ToolMessageEvent;

declare type Model = {
    id: string;
    name: string;
    preview?: boolean;
    capabilities: {
        supports: {
            vision: boolean;
        };
        limits: {
            max_prompt_tokens?: number;
            max_output_tokens?: number;
            max_context_window_tokens: number;
            vision?: {
                supported_media_types: string[];
                max_prompt_images: number;
                max_prompt_image_size: number;
            };
        };
    };
    policy?: {
        state: "enabled" | "disabled" | "unconfigured";
        terms: string;
    };
    billing?: {
        multiplier: number;
    };
};

declare type ModelCallFailureEvent = {
    kind: "model_call_failure";
    turn: number;
    callId?: string;
    modelCallDurationMs: number;
    /**
     * The model call that failed, if available.
     */
    modelCall: ModelCallParam;
    /**
     * A string representation of the messages sent as input to the model call, if available.
     */
    requestMessages?: string;
};

/**
 * -----------------------------------------------------------------------
 * Events
 * -----------------------------------------------------------------------
 *
 * Event (Union Type)
 * ├── TurnStartedEvent
 * ├── TurnEndedEvent
 * ├── TurnFailedEvent
 * ├── TurnRetryEvent
 * ├── ModelCallSuccessEvent
 * ├── ModelCallFailureEvent
 * ├── ToolExecutionEvent
 * ├── ImageProcessingEvent
 * ├── ImageRemovalEvent
 * ├── TruncationEvent
 * ├── MessageEvent
 * │   ├── AssistantMessageEvent
 * │   ├── UserMessageEvent
 * │   └── ToolMessageEvent
 * ├── ResponseEvent
 * └── SessionLogEvent
 */
/**
 * All types of events that can be emitted by the `Client`.
 */
declare interface ModelCallParam {
    api_id?: string;
    model?: string;
    error?: string;
    status?: number;
    request_id?: string;
    initiator?: string;
}

declare type ModelCallSuccessEvent = {
    kind: "model_call_success";
    turn: number;
    callId?: string;
    modelCallDurationMs: number;
    /**
     * Time to first token in milliseconds. Only available for streaming requests.
     */
    ttftMs?: number;
    /**
     * Average inter-token latency in milliseconds. Only available for streaming requests.
     * Calculated as the average time between successive tokens.
     */
    interTokenLatencyMs?: number;
    modelCall: ModelCallParam;
    responseChunk: CopilotChatCompletionChunk;
    responseUsage: OpenAI.ChatCompletion["usage"];
    /**
     * A string representation of the messages sent as input to the model call, if available.
     */
    requestMessages?: string;
    quotaSnapshots?: Record<string, QuotaSnapshot>;
    /**
     * GitHub's request tracing ID (x-github-request-id header) for this model call.
     */
    requestId?: string;
};

/**
 * Model response choice.
 * Maps to: copilot_cli.v0.entities.ModelChoice (model_choice.proto)
 */
declare interface ModelChoice {
    /** Reason the model stopped generating (e.g., "stop", "tool_calls") */
    finish_reason: string;
    /** Generated text content */
    content?: string;
    /** Model refusal message */
    refusal?: string;
    /** Tool call IDs referenced in this choice */
    tool_call_ids?: string[];
    /** Tool calls requested by the model in this choice */
    tool_calls?: ModelChoiceToolCall[];
}

/**
 * Tool call details within model response choices.
 * Maps to: copilot_cli.v0.entities.ModelChoiceToolCall (model_choice_tool_call.proto)
 */
declare interface ModelChoiceToolCall {
    /** Tool call ID */
    tool_call_id: string;
    /** Name of the tool */
    tool_name: string;
    /** Tool call arguments (JSON string) */
    arguments: string;
}

/**
 * Per-model metrics tracked during a session
 */
export declare interface ModelMetrics {
    requests: {
        count: number;
        cost: number;
    };
    usage: {
        inputTokens: number;
        outputTokens: number;
        cacheReadTokens: number;
        cacheWriteTokens: number;
    };
}

export declare class NoopLogger extends BaseLogger implements RunnerLogger {
    constructor();
    debug(_message: string): void;
    log(_message: string): void;
    info(_message: string): void;
    notice(_message: string | Error): void;
    warning(_message: string | Error): void;
    error(_message: string | Error): void;
    startGroup(_name: string, _level?: LogLevel): void;
    endGroup(_level?: LogLevel): void;
}

declare type OnRequestErrorContext = {
    /**
     * The current turn.
     */
    readonly turn: number;
    /**
     * The current retry attempt.
     */
    readonly retry: number;
    /**
     * The maximum number of retry attempts.
     */
    readonly maxRetries: number;
    /**
     * The error received in response to a request.
     */
    readonly error: unknown;
    /**
     * The HTTP status code from the error response, if available.
     */
    readonly status: number | undefined;
    /**
     * Information about the model being called.
     */
    readonly modelInfo: CompletionWithToolsModel;
    /**
     * The current {@link GetCompletionWithToolsOptions}
     */
    readonly getCompletionWithToolsOptions: GetCompletionWithToolsOptions | undefined;
};

declare type OnRequestErrorResult = {
    /**
     * If the processor does something to handle the error and wishes for there to be a retry
     * it should set this to how many milliseconds to wait before retrying.
     */
    retryAfter: number;
    /** Why this retry is happening (e.g., "snippy_annotations"). Defaults to "streaming_error" if not set. */
    retryReason?: string;
};

export declare type PendingMessagesModifiedEvent = z_2.infer<typeof PendingMessagesModifiedEventSchema>;

/**
 * User message queued (ephemeral event for UI updates)
 */
declare const PendingMessagesModifiedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"pending_messages.modified">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    ephemeral: true;
    type: "pending_messages.modified";
    timestamp: string;
    parentId: string | null;
}, {
    data: {};
    id: string;
    ephemeral: true;
    type: "pending_messages.modified";
    timestamp: string;
    parentId: string | null;
}>;

/**
 * Represents a pending queued item for UI display purposes.
 */
export declare interface PendingQueuedItem {
    kind: "message" | "command";
    displayText: string;
}

/**
 * A permission request which will be used to check tool or path usage against config and/or request user approval.
 */
declare type PermissionRequest = {
    toolCallId?: string;
} & (ShellPermissionRequest | WritePermissionRequest | MCPPermissionRequest | ReadPermissionRequest | UrlPermissionRequest | MemoryPermissionRequest);

/**
 * The result of requesting permissions.
 */
declare type PermissionRequestResult = {
    readonly kind: "approved";
} | {
    readonly kind: "denied-by-rules";
    rules: ReadonlyArray<Rule>;
} | {
    readonly kind: "denied-no-approval-rule-and-could-not-request-from-user";
} | {
    readonly kind: "denied-interactively-by-user";
    readonly feedback?: string;
} | {
    readonly kind: "denied-by-content-exclusion-policy";
    readonly path: string;
    readonly message: string;
};

/**
 * Configuration for permissions handling.
 *
 * For CCA, permissions requests are not required.
 */
declare type PermissionsConfig = {
    requestRequired: false;
} | {
    requestRequired: true;
    request: RequestPermissionFn;
};

/**
 * This is just a type to warn that there's a good chance it's not a real path, because
 * it was _very_ heuristically parsed out of a command.
 */
declare type PossiblePath = string;

/**
 * A possible URL that was heuristically parsed from a command.
 */
declare type PossibleUrl = {
    readonly url: string;
};

declare type PostRequestContext = {
    /**
     * An identifier for the completion with tools call. This can be used for logging
     * and tracing purposes.
     */
    readonly callId: string | undefined;
    /**
     * The current turn.
     */
    readonly turn: number;
    /**
     * Information about the model being called.
     */
    readonly modelInfo: CompletionWithToolsModel;
    /**
     * The parsed response messages from the model.
     */
    readonly responseMessages: ChatCompletionMessageParam[];
    /**
     * The current {@link GetCompletionWithToolsOptions}
     */
    readonly getCompletionWithToolsOptions: GetCompletionWithToolsOptions | undefined;
};

/**
 * Result type for postRequest processors.
 * Currently empty as processors signal retries by throwing errors that are caught
 * by onRequestError processors. Reserved for future use.
 */
declare type PostRequestResult = Record<string, never>;

declare type PostToolExecutionContext = {
    /**
     * The current turn.
     */
    readonly turn: number;
    /**
     * What tool call was executed.
     */
    readonly toolCall: CopilotChatCompletionMessageToolCall;
    /**
     * The result of the tool call. Can be modified in place by the processor.
     */
    readonly toolResult: ToolResultExpanded;
    /**
     * Information about the model being called.
     */
    readonly modelInfo: CompletionWithToolsModel;
};

export declare type PostToolUseHook = (input: PostToolUseHookInput) => Promise<PostToolUseHookOutput | void>;

/**
 * Post-tool use hook types
 */
export declare interface PostToolUseHookInput extends BaseHookInput {
    toolName: string;
    toolArgs: unknown;
    toolResult: ToolResultExpanded;
}

export declare interface PostToolUseHookOutput {
    modifiedResult?: ToolResultExpanded;
    additionalContext?: string;
    suppressOutput?: boolean;
}

declare type PreRequestContext = {
    /**
     * An identifier for the completion with tools call. This can be used for logging
     * and tracing purposes.
     */
    readonly callId: string | undefined;
    /**
     * The current turn.
     */
    readonly turn: number;
    /**
     * The current retry attempt.
     */
    readonly retry: number;
    /**
     * The messages that will be sent to the model for the request.
     * If modifying them, do so in place.
     */
    readonly messages: ChatCompletionMessageParam[];
    /**
     * The tool definitions that will be sent to the model for the request.
     * These should not be modified.
     */
    readonly toolDefinitions: ChatCompletionTool[];
    /**
     * Information about the model being called.
     */
    readonly modelInfo: CompletionWithToolsModel;
    /**
     * Additional headers to send with the request. If adding additional headers
     * then simply add to this object.
     */
    readonly additionalRequestHeaders: Record<string, string>;
    /**
     * The current {@link GetCompletionWithToolsOptions}
     */
    readonly getCompletionWithToolsOptions: GetCompletionWithToolsOptions | undefined;
    /**
     * The client that can be used to make additional LLM calls (e.g., for compaction).
     */
    readonly client: Client_2;
    /**
     * The rich tool objects with callbacks, needed for nested LLM calls.
     */
    readonly tools: Tool_2[];
};

declare type PreToolsExecutionContext = {
    /**
     * The current turn.
     */
    readonly turn: number;
    /**
     * What tool calls are being executed.
     */
    readonly toolCalls: CopilotChatCompletionMessageToolCall[];
    /**
     * Information about the model being called.
     */
    readonly modelInfo: CompletionWithToolsModel;
};

/**
 * Pre-tools execution procesors can either return nothing, or results for one or more
 * of the tools to be executed. These results will be given to the model in lieu of
 * performing the tool call and obtaining a result from the tool itself.
 */
declare type PreToolsExecutionResult = void | Map<string, ToolResultExpanded>;

export declare type PreToolUseHook = (input: PreToolUseHookInput) => Promise<PreToolUseHookOutput | void>;

/**
 * Pre-tool use hook types
 */
export declare interface PreToolUseHookInput extends BaseHookInput {
    toolName: string;
    toolArgs: unknown;
}

export declare interface PreToolUseHookOutput {
    permissionDecision?: "allow" | "deny" | "ask";
    permissionDecisionReason?: string;
    modifiedArgs?: unknown;
    additionalContext?: string;
    suppressOutput?: boolean;
}

/**
 * Processor that executes preToolUse hooks and can deny tool execution or modify arguments.
 * Returns denied results for tools that hooks block.
 * Mutates tool call arguments in place when hooks return modifiedArgs.
 */
export declare class PreToolUseHooksProcessor implements IPreToolsExecutionProcessor {
    private readonly hooks;
    private readonly workingDir;
    constructor(hooks: QueryHooks | undefined, workingDir: string);
    toJSON(): string;
    preToolsExecution(context: PreToolsExecutionContext): Promise<Map<string, ToolResultExpanded> | void>;
}

/**
 * A single progress line for a background agent task.
 */
export declare type ProgressLine = {
    /** Display message, e.g., "▸ bash", "✓ edit src/foo.ts" */
    message: string;
    /** When this event occurred */
    timestamp: number;
};

/**
 * Configuration for a custom API provider (BYOK - Bring Your Own Key).
 * When set, bypasses Copilot API authentication and uses this provider instead.
 */
export declare interface ProviderConfig {
    /** Provider type. Defaults to "openai" for generic OpenAI-compatible APIs. */
    type?: "openai" | "azure" | "anthropic";
    /** Wire API format (openai/azure only). Defaults to "completions". */
    wireApi?: "completions" | "responses";
    /** API endpoint URL */
    baseUrl: string;
    /** API key. Optional for local providers like Ollama. */
    apiKey?: string;
    /**
     * Bearer token for authentication. Sets the Authorization header directly.
     * Use this for services requiring bearer token auth instead of API key.
     * Takes precedence over apiKey when both are set.
     */
    bearerToken?: string;
    /** Azure-specific options */
    azure?: {
        /** API version. Defaults to "2024-10-21". */
        apiVersion?: string;
    };
}

/** Result of a session prune operation. */
declare interface PruneResult {
    /** Session IDs that were deleted (empty in dry-run mode). */
    deleted: string[];
    /** Session IDs that would be deleted in dry-run mode (empty when not dry-run). */
    candidates: string[];
    /** Session IDs that were skipped (e.g., named sessions). */
    skipped: string[];
    /** Total bytes freed (actual) or that would be freed (dry-run). */
    freedBytes: number;
    /** Whether this was a dry run (no actual deletions). */
    dryRun: boolean;
}

/**
 * Functional query API that provides a Claude Code-inspired interface.
 * This is a thin wrapper around the Session class for simple use cases.
 *
 * @param options - Query configuration options
 * @returns AsyncGenerator of SessionEvent objects
 */
export declare function query(options: QueryOptions): AsyncIterable<SessionEvent>;

/**
 * Hook system with arrays of specific hook callbacks
 */
export declare interface QueryHooks {
    preToolUse?: PreToolUseHook[];
    postToolUse?: PostToolUseHook[];
    userPromptSubmitted?: UserPromptSubmittedHook[];
    sessionStart?: SessionStartHook[];
    sessionEnd?: SessionEndHook[];
    errorOccurred?: ErrorOccurredHook[];
    agentStop?: AgentStopHook[];
    subagentStop?: SubagentStopHook[];
}

export declare type QueryOptions = SessionOptions & {
    prompt: string;
    abortController?: AbortController;
};

/**
 * Callback type for handling queued slash commands.
 * Returns whether the command was handled and whether to stop processing the queue.
 */
export declare type QueuedCommandHandler = (command: string) => Promise<QueuedCommandResult>;

/**
 * Represents a queued slash command to be executed.
 */
export declare interface QueuedCommandItem {
    kind: "command";
    /** The full command string including the slash, e.g., "/compact" or "/model gpt-4" */
    command: string;
}

/**
 * Result from executing a queued slash command.
 * - `stopProcessingQueue`: If true, stops processing remaining items in this session's queue
 *   (used when /clear or /exit wipes the queue)
 */
export declare type QueuedCommandResult = {
    handled: true;
    stopProcessingQueue?: boolean;
} | {
    handled: false;
};

/**
 * A unified queue item that can represent either a user message or a slash command.
 * Used to support queueing slash commands alongside messages in FIFO order.
 */
export declare type QueuedItem = QueuedMessageItem | QueuedCommandItem;

/**
 * Represents a queued user message to be processed by the agentic loop.
 */
export declare interface QueuedMessageItem {
    kind: "message";
    options: SendOptions;
}

declare type QuotaSnapshot = {
    /**
     * Whether or not it's an unlimited entitlement.
     */
    isUnlimitedEntitlement: boolean;
    /**
     * The number of requests included in the entitlement, or "-1" for unlimited
     * entitlement, so that the user/client can understand how much they get each
     * month/period; the value is an integer
     */
    entitlementRequests: number;
    /**
     * The count of requests used so far in this month/period, so that the
     * user/client can understand how much of their entitlement they have used;
     * the value is an integer
     */
    usedRequests: number;
    /**
     * Indicates whether usage is allowed once quota is exhausted, so that the
     * user/client can understand if they can continue usage at a pay-per-request
     * rate when entitlement is exhausted; the value is boolean
     */
    usageAllowedWithExhaustedQuota: boolean;
    /**
     * The count of overage requests made so far in this month/period, so that
     * the user/client can understand how much they have spent in pay-per-request
     * charges so far this month/period; the value is a decimal
     */
    overage: number;
    /**
     * Indicates whether overage is allowed once quota is exhausted, so that the
     * user/client can understand if they can continue usage at a pay-per-request
     * rate when entitlement is exhausted; the value is boolean
     */
    overageAllowedWithExhaustedQuota: boolean;
    /**
     * The percentage of the entitlement remaining at the snapshot timestamp, so
     * that the user/client can understand how much they have remaining and their
     * rate of usage; the value is a decimal
     */
    remainingPercentage: number;
    /**
     * The date when the quota resets, so that the user/client can know when they
     * next receive their entitlement; the value is an RFC3339 formatted UTC date;
     * if the entitlement is unlimited, this value is not included in the snapshot
     */
    resetDate?: Date;
};

/**
 * A permission request for reading file or directory contents.
 */
declare type ReadPermissionRequest = {
    readonly kind: "read";
    /** The intention of the edit operation, e.g. "Read file" or "List directory" */
    readonly intention: string;
    /** The path of the file or directory being read */
    readonly path: string;
};

declare type ReasoningMessageParam = {
    /**
     * An ID or encrypted value that allows the model to restore
     */
    reasoning_opaque?: string;
    /**
     * Human-readable text describing the model's thinking process.
     */
    reasoning_text?: string;
    /**
     * An encrypted representation of the model's internal state
     */
    encrypted_content?: string | undefined | null;
};

/**
 * Human-readable labels for each relevance score level.
 */
declare const RELEVANCE_LABELS: Record<RelevanceScore, string>;

/**
 * Relevance score for a session relative to the current working directory.
 * Higher scores indicate more relevant sessions.
 */
declare type RelevanceScore = 0 | 1 | 2 | 3 | 4;

export declare class RemoteSession extends Session<RemoteSessionMetadata> {
    readonly repository: {
        name: string;
        owner: string;
        branch: string;
    };
    readonly remoteSessionIds: string[];
    readonly pullRequestNumber?: number;
    constructor(options: SessionOptions & {
        repository: {
            name: string;
            owner: string;
            branch: string;
        };
        remoteSessionIds: string[];
        pullRequestNumber?: number;
    });
    send(options: SendOptions): Promise<void>;
    abort(): Promise<void>;
    compactHistory(): Promise<CompactionResult>;
    getMetadata(): RemoteSessionMetadata;
}

export declare interface RemoteSessionMetadata extends SessionMetadata {
    readonly repository: {
        owner: string;
        name: string;
        branch: string;
    };
    readonly remoteSessionIds: string[];
    readonly pullRequestNumber?: number;
    readonly isRemote: true;
}

declare type RequestPermissionFn = (permission: PermissionRequest) => Promise<PermissionRequestResult>;

export declare type ResourceLink = z_2.infer<typeof ResourceLinkSchema>;

/**
 * Resource link content block
 */
declare const ResourceLinkSchema: z_2.ZodObject<{
    icons: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
        src: z_2.ZodString;
        mimeType: z_2.ZodOptional<z_2.ZodString>;
        sizes: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
        theme: z_2.ZodOptional<z_2.ZodEnum<["light", "dark"]>>;
    }, "strip", z_2.ZodTypeAny, {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }, {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }>, "many">>;
    name: z_2.ZodString;
    title: z_2.ZodOptional<z_2.ZodString>;
    uri: z_2.ZodString;
    description: z_2.ZodOptional<z_2.ZodString>;
    mimeType: z_2.ZodOptional<z_2.ZodString>;
    size: z_2.ZodOptional<z_2.ZodNumber>;
} & {
    type: z_2.ZodLiteral<"resource_link">;
}, "strip", z_2.ZodTypeAny, {
    name: string;
    type: "resource_link";
    uri: string;
    mimeType?: string | undefined;
    size?: number | undefined;
    description?: string | undefined;
    title?: string | undefined;
    icons?: {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }[] | undefined;
}, {
    name: string;
    type: "resource_link";
    uri: string;
    mimeType?: string | undefined;
    size?: number | undefined;
    description?: string | undefined;
    title?: string | undefined;
    icons?: {
        src: string;
        mimeType?: string | undefined;
        theme?: "light" | "dark" | undefined;
        sizes?: string[] | undefined;
    }[] | undefined;
}>;

/**
 * An event that is emitted by the `Client` which contains the final response from the LLM.
 */
declare type ResponseEvent = {
    kind: "response";
    turn?: number;
    callId?: string;
    modelCall?: ModelCallParam;
    response: ChatCompletionMessage;
};

/**
 * Retrieves the list of available models based on policies and integration including capabilities and
 * billing information, which may be cached from previous calls.
 *
 * This list is in order of preference to be the default model for new sessions where the first model is
 * the most preferred.  It can be empty if all model policies are disabled.
 */
export declare function retrieveAvailableModels(authInfo: AuthInfo, copilotUrl: string | undefined, integrationId: string, sessionId: string, logger: RunnerLogger): Promise<{
    models: Model[];
    copilotUrl: string | undefined;
}>;

/**
 * A Rule defines a pattern for matching permission requests.
 *
 * It is unfortunately generically named because it is intended to match across
 * different types of tool uses, e.g. `Shell(touch)` or `GitHubMCP(list_issues)`,
 * `view(.env-secrets)`
 */
declare type Rule = {
    /**
     * The kind of rule that should be matched e.g. `Shell` or `GitHubMCP`.
     */
    readonly kind: string;
    /**
     * If null, matches all arguments to the kind.
     */
    readonly argument: string | null;
};

export declare interface RunnerLogger {
    /**
     * Log a message ignoring the configured log level.
     * This is useful for logging messages that should always be logged, regardless of the log level.
     * @param message The message to log.
     */
    log(message: string): void;
    /**
     * Returns true if the environment is set to debug.
     * Note: This is not the same as the log level being set to debug.
     */
    isDebug(): boolean;
    /**
     * Log a debug message. This is only logged if the log level is set to debug.
     * @param message The message to log.
     */
    debug(message: string): void;
    /**
     * Log an info message. This is only logged if the log level is set to info or debug.
     * @param message The message to log.
     */
    info(message: string): void;
    /**
     * Log a notice message. This is only logged if the log level is set to warning, info, or debug,
     * but logs using the logger's info method.
     * This is useful for logging messages that are not errors, but are important enough to log on
     * less verbose log levels.
     * @param message The message to log.
     */
    notice(message: string | Error): void;
    /**
     * Log a warning message. This is only logged if the log level is set to warning, info, or debug
     * @param message The message to log.
     */
    warning(message: string | Error): void;
    /**
     * Log an error message. This is only logged if the log level is set to error, warning, info, or debug
     * @param message The message to log.
     */
    error(message: string | Error): void;
    /**
     * Log a message that starts a new group.
     * @param name The name of the group.
     * @param level The log level of the group. Defaults to info.
     */
    startGroup(name: string, level?: LogLevel): void;
    /**
     * Log a message that ends the current group.
     * @param level The log level of the group. Defaults to info.
     */
    endGroup(level?: LogLevel): void;
}

declare type RuntimeSettings = DeepPartial<IRuntimeSettings>;

/**
 * Indicates the safety level of the assessed script.
 *
 * There may some cases where a script cannot be assessed (e.g. unparseable),
 * in which case we bail with a failure.
 */
declare type SafetyAssessment = {
    readonly result: "failed";
    /**
     * A human-readable reason why the safety assessment could not be completed.
     */
    readonly reason: string;
} | {
    readonly result: "completed";
    readonly commands: ReadonlyArray<AssessedCommand>;
    /**
     * Possible absolute file paths that the script might operate on, based on heuristic parsing.
     */
    readonly possiblePaths: ReadonlyArray<PossiblePath>;
    /**
     * Possible URLs that the script might access, based on heuristic parsing.
     */
    readonly possibleUrls: ReadonlyArray<PossibleUrl>;
    /**
     * Indicates whether any command in the script has redirection to write to a file.
     */
    readonly hasWriteFileRedirection: boolean;
    /**
     * Indicates whether a command can be approved for the rest of the running session.
     *
     * Simple commands like `git status` or `npm test` can be session approved
     * because their impact is predictable. Complex commands with substitutions,
     * variables, or side effects require per-invocation approval.
     *
     * Examples of session-approvable: `ls`, `git log`, `command1 && command2`
     * Examples requiring per-invocation: `rm -rf $DIR`, `find -exec`
     */
    readonly canOfferSessionApproval: boolean;
    /**
     * Optional warning message to display to the user (e.g., when parser is unavailable).
     * This should be shown in the permission dialog and/or timeline.
     */
    readonly warning?: string;
};

/**
 * Session metadata with its relevance score for display grouping.
 */
declare interface ScoredSessionMetadata<T extends SessionMetadata = SessionMetadata> {
    session: T;
    score: RelevanceScore;
}

/**
 * Scores the relevance of a session's context to the current working directory context.
 * Higher scores indicate more relevant sessions.
 *
 * Scoring:
 * - 4: Same repository + same branch (exact match)
 * - 3: Same repository, different branch
 * - 2: Same git root (for non-GitHub repos)
 * - 1: Same working directory (for non-git directories)
 * - 0: No match
 */
declare function scoreSessionRelevance(sessionContext: SessionContext | undefined, currentContext: WorkingDirectoryContext): RelevanceScore;

export declare type SelectionAttachment = z_2.infer<typeof SelectionAttachmentSchema>;

declare const SelectionAttachmentSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"selection">;
    filePath: z_2.ZodString;
    displayName: z_2.ZodString;
    text: z_2.ZodString;
    selection: z_2.ZodObject<{
        start: z_2.ZodObject<{
            line: z_2.ZodNumber;
            character: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            line: number;
            character: number;
        }, {
            line: number;
            character: number;
        }>;
        end: z_2.ZodObject<{
            line: z_2.ZodNumber;
            character: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            line: number;
            character: number;
        }, {
            line: number;
            character: number;
        }>;
    }, "strip", z_2.ZodTypeAny, {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    }, {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    }>;
}, "strip", z_2.ZodTypeAny, {
    type: "selection";
    text: string;
    filePath: string;
    displayName: string;
    selection: {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    };
}, {
    type: "selection";
    text: string;
    filePath: string;
    displayName: string;
    selection: {
        end: {
            line: number;
            character: number;
        };
        start: {
            line: number;
            character: number;
        };
    };
}>;

export declare interface SendOptions {
    prompt: string;
    /** If provided, this is shown in the timeline instead of prompt */
    displayPrompt?: string;
    attachments?: Attachment[];
    mode?: "enqueue" | "immediate";
    abortController?: AbortController;
    /**
     * If true, adds the message to the front of the queue instead of the end.
     * This is useful when a queued command (e.g., /plan, /review) returns an agentMessage
     * that should be processed immediately after the command, before other queued items.
     * Without this, the agentMessage would be appended to the end and processed after
     * any other items already in the queue.
     */
    prepend?: boolean;
    /**
     * If set to false, this message won't trigger a PRU (Premium Request Unit) charge.
     * User messages default to billable. Set to false to override.
     */
    billable?: boolean;
    /** The agent mode active when this message was sent (interactive, plan, autopilot) */
    agentMode?: AgentMode;
    /** Source identifier for this message. "system" indicates runtime-generated notifications that should be hidden from timeline. */
    source?: "system";
}

declare type ServerConnectionStatus = "connected" | "failed" | "pending" | "not_configured";

declare interface ServerFailureInfo {
    error: Error;
    timestamp: number;
}

/**
 * Interface for reporting server connection status changes.
 * Implement this interface to receive notifications when servers start connecting,
 * connect successfully, or fail to connect.
 */
declare interface ServerStatusCallback {
    onServerStatus(serverName: string, event: ServerStatusEvent): void;
}

/**
 * Server status event types for the status callback.
 */
declare type ServerStatusEvent = {
    status: "starting";
} | {
    status: "connected";
} | {
    status: "failed";
    error: Error;
};

export declare abstract class Session<SM extends SessionMetadata = SessionMetadata> implements ApiSchemaToInterface<typeof sessionApiSchema> {
    readonly model: ApiSchemaToInterface<{
        getCurrent: {
            result: ZodObject<    {
            modelId: ZodOptional<ZodString>;
            }, "strip", ZodTypeAny, {
            modelId?: string | undefined;
            }, {
            modelId?: string | undefined;
            }>;
        };
        switchTo: {
            params: ZodObject<    {
            modelId: ZodString;
            }, "strip", ZodTypeAny, {
            modelId: string;
            }, {
            modelId: string;
            }>;
            result: ZodObject<    {
            modelId: ZodOptional<ZodString>;
            }, "strip", ZodTypeAny, {
            modelId?: string | undefined;
            }, {
            modelId?: string | undefined;
            }>;
        };
    }>;
    readonly sessionId: string;
    readonly startTime: Date;
    readonly modifiedTime: Date;
    readonly summary?: string;
    /** Get the resolved feature flags for this session */
    get resolvedFeatureFlags(): FeatureFlags | undefined;
    /** Whether experimental mode is enabled for this session */
    isExperimentalMode: boolean;
    /** Get the installed plugins for this session */
    getInstalledPlugins(): readonly InstalledPlugin[] | undefined;
    private events;
    protected _chatMessages: ChatCompletionMessageParam[];
    protected _selectedModel: string | undefined;
    protected originalUserMessages: string[];
    /** Skills invoked during this session, for preservation across compaction */
    protected invokedSkills: InvokedSkillInfo[];
    private compactionCheckpointLength;
    private compactionCheckpointEventIndex;
    private eventProcessingQueue;
    private eventHandlers;
    private wildcardEventHandlers;
    private usageMetricsTracker;
    protected integrationId: string;
    protected availableTools?: string[];
    protected excludedTools?: string[];
    protected executeToolsInParallel?: boolean;
    protected shellConfig?: ShellConfig;
    protected requestPermission?: (permissionRequest: PermissionRequest) => Promise<PermissionRequestResult>;
    protected mcpServers?: Record<string, MCPServerConfig>;
    protected mcpHost?: McpHost;
    protected selectedAgentMcpServerNames: Set<string>;
    protected lastInitializedAgentMcpServers?: Record<string, MCPServerConfig>;
    protected hooks?: QueryHooks;
    protected customAgents?: SweCustomAgent[];
    protected selectedCustomAgent?: SweCustomAgent;
    protected organizationCustomInstructions?: string;
    protected skipCustomInstructions: boolean;
    protected disabledInstructionSources?: ReadonlySet<string>;
    protected systemMessageConfig?: SystemMessageConfig;
    protected workingDir: string;
    protected featureFlags?: FeatureFlags;
    protected skillDirectories?: string[];
    protected disabledSkills?: Set<string>;
    protected installedPlugins?: InstalledPlugin[];
    protected onFileCreated?: (path: string) => void;
    protected onWarning?: (message: string) => void;
    protected requestUserInput?: (request: {
        question: string;
        choices?: string[];
        allowFreeform?: boolean;
    }) => Promise<{
        answer: string;
        wasFreeform: boolean;
    }>;
    protected askUserDisabled?: boolean;
    protected onTaskComplete?: (summary?: string) => void;
    protected telemetryHandler?: (event: TelemetryEvent_2) => void;
    protected runningInInteractiveMode?: boolean;
    protected sessionCapabilities?: Set<SessionCapability>;
    protected getContentExclusionService?: () => ContentExclusionService | undefined;
    protected trajectoryFile?: string;
    protected eventsLogDirectory?: string;
    protected transcriptPath?: string;
    protected authInfo?: AuthInfo;
    protected copilotUrl?: string;
    protected provider?: ProviderConfig;
    protected reasoningEffort?: "low" | "medium" | "high" | "xhigh";
    protected onToolsUpdateCallbacks: ((tools: Tool_2[]) => void)[];
    protected onBackgroundTaskChangeCallbacks: (() => void)[];
    protected enableStreaming: boolean;
    protected handoffContext?: string;
    protected externalToolDefinitions?: ExternalToolDefinition[];
    protected externalToolDispatcher?: ExternalToolDispatcher;
    protected clientName?: string;
    protected largeOutputConfig?: LargeToolOutputConfig;
    protected runtimeSettings?: RuntimeSettings;
    protected configDir?: string;
    protected repositoryName?: string;
    protected workspaceContext?: WorkspaceContextInfo;
    protected readonly backgroundAgentRegistry: BackgroundAgentRegistry;
    protected readonly detachedShellRegistry: DetachedShellRegistry;
    protected currentSystemMessage?: string;
    protected currentToolMetadata?: ToolMetadata[];
    protected currentTools?: Tool_2[];
    protected previousModelUsed?: string;
    abstract send(options: SendOptions): Promise<void>;
    abstract abort(): Promise<void>;
    /**
     * Check if the session is currently in a state where it can be aborted.
     * Returns true if there's an active operation that can be cancelled.
     * Default implementation returns false. Override in subclasses that support abortion.
     */
    isAbortable(): boolean;
    /**
     * Initializes tools and validates tool filter configuration.
     * This method should be called after the session is fully configured (auth, model, MCP servers)
     * but before the first message is sent. It will emit warnings for any unknown tool names
     * specified in availableTools or excludedTools.
     *
     * Default implementation is a no-op. Override in subclasses that support tool validation.
     *
     * @returns Promise that resolves when initialization and validation is complete
     */
    initializeAndValidateTools(): Promise<void>;
    constructor(options?: SessionOptions);
    abstract getMetadata(): SM;
    /**
     * Get the current usage metrics for this session.
     * Returns aggregated metrics including premium requests, tokens, and code changes.
     */
    get usageMetrics(): UsageMetrics;
    /**
     * Emit a session.shutdown event with the current usage metrics.
     * This is an ephemeral event.
     *
     * @param shutdownType - Whether this is a routine shutdown or error shutdown
     */
    shutdown(shutdownType?: "routine" | "error", errorReason?: string): void;
    static fromEvents<S extends Session, O extends SessionOptions = SessionOptions>(this: new (_options: O) => S, events: SessionEvent[], options?: O): Promise<S>;
    /**
     * Updates the authentication information for this session.
     * This is a convenience method equivalent to updateOptions({ authInfo }).
     *
     * @param authInfo - The new authentication information to use for this session
     */
    setAuthInfo(authInfo: AuthInfo | undefined): void;
    /**
     * Updates session options after creation.
     * This method allows selectively updating configuration options without recreating the session.
     * Only the provided options will be updated; omitted options remain unchanged.
     *
     * @param options - Partial session options to update
     *
     * @example
     * ```typescript
     * // Update multiple options at once
     * session.updateOptions({
     *   logger: fileLogger,
     *   mcpServers: mcpConfig,
     *   customAgents: loadedAgents
     * });
     *
     * // Or use convenience methods for single updates
     * session.setAuthInfo(newAuthInfo); // Preferred for auth
     * session.setSelectedModel(newModel); // Preferred for model
     * ```
     */
    updateOptions(options: Partial<UpdatableSessionOptions>): void;
    /**
     * Register a callback to be notified when tools are initialized/updated.
     * This is called during the agentic loop after tools are built and available.
     * Useful for UI components that need to display tool information.
     * Multiple callbacks can be registered and each will be called when tools update.
     *
     * @param callback - Function to call with the available tools
     * @returns A function to unregister the callback
     *
     * @example
     * ```typescript
     * const unregister = session.onToolsUpdate((tools) => {
     *   console.log(`${tools.length} tools available`);
     *   updateToolsList(tools);
     * });
     * // Later, to stop receiving updates:
     * unregister();
     * ```
     */
    onToolsUpdate(callback: (tools: Tool_2[]) => void): () => void;
    /**
     * Set the telemetry handler for this session.
     * Called by LocalSessionManager after session creation.
     *
     * @param handler - Function to handle telemetry events
     */
    setTelemetryHandler(handler: (event: TelemetryEvent_2) => void): void;
    /**
     * Send a telemetry event if telemetry is configured.
     * This is a no-op if no telemetry handler was set via setTelemetryHandler().
     *
     * @param event - The telemetry event to send
     */
    sendTelemetry(event: TelemetryEvent_2): void;
    /**
     * Get list of available custom agents.
     * Returns the custom agents that were provided when the session was created.
     *
     * @returns Array of available custom agents
     */
    getAvailableCustomAgents(): SweCustomAgent[];
    /**
     * Get the custom configuration directory for this session.
     * When set, this overrides the default Copilot config directory (~/.copilot or $XDG_CONFIG_HOME/.copilot).
     *
     * @returns The custom configuration directory, or undefined if using the default
     */
    getConfigDir(): string | undefined;
    /**
     * Get all background tasks (agents and shells) started by this session.
     * Returns a unified array with type discriminator for each task.
     */
    getBackgroundTasks(): BackgroundTask[];
    private getToolCallSummary;
    /**
     * Gets progress information for a background task.
     * For agent tasks, derives progress from session events filtered by parentToolCallId.
     * For shell tasks, reads recent output from the log file.
     *
     * @param task - The background task to get progress for
     * @returns Progress information discriminated by task type
     */
    getBackgroundTaskProgress(task: BackgroundTask): Promise<BackgroundTaskProgress>;
    /**
     * Cancel or kill a background task by ID.
     * For agents, this cancels the execution (soft cancel).
     * For shells, this kills the process (hard kill).
     *
     * @param taskId - The task ID (agent ID or shell ID)
     * @returns true if the task was found and cancelled/killed, false otherwise
     */
    cancelBackgroundTask(taskId: string): Promise<boolean>;
    /**
     * Refresh the status of all background tasks.
     * This updates shell statuses by checking if their processes are still running.
     * Agent statuses are updated automatically via their completion callbacks.
     */
    refreshBackgroundTasks(): Promise<void>;
    /**
     * Remove a finalized background task from tracking.
     * Only removes tasks that are not running (completed, failed, cancelled, killed).
     *
     * @param taskId - The task ID (agent ID or shell ID)
     * @returns true if the task was found and removed, false otherwise
     */
    removeBackgroundTask(taskId: string): boolean;
    /**
     * Register a callback to be notified when background tasks change.
     * Called when a task is started, completes, or is removed.
     * Useful for UI components that need to update when tasks change.
     *
     * @param callback - Function to call when tasks change
     * @returns A function to unregister the callback
     */
    onBackgroundTaskChange(callback: () => void): () => void;
    /**
     * Notify all registered callbacks that background tasks have changed.
     * Called internally when tasks start or complete.
     */
    protected notifyBackgroundTaskChange(): void;
    protected getRuntimeSettings(): RuntimeSettings;
    /**
     * Resolves the repository name to use for settings.
     * Returns the explicitly configured repositoryName if set,
     * otherwise attempts to detect it from the git remote in the working directory.
     *
     * @returns The repository name in "owner/repo" format, or undefined if not available
     */
    protected resolveRepositoryName(): Promise<string | undefined>;
    /**
     * Select a custom agent for subsequent queries.
     * When a custom agent is selected, only that agent's tools (and required tools) will be available.
     * The selected custom agent will still be available via the task tool.
     *
     * @param agentId - The name/id of the custom agent to select
     * @throws Error if the agent is not found
     */
    selectCustomAgent(agentId: string): Promise<void>;
    /**
     * Clear custom agent selection.
     * After calling this, all tools will be available again.
     */
    clearCustomAgent(): void;
    /**
     * Registers an event handler for specific event types or all events.
     * Supports both synchronous and asynchronous handlers.
     *
     * @param eventType - The event type to listen for (e.g., "assistant.message", "tool.execution_complete") or "*" for all events
     * @param handler - The handler function to call when the event is emitted. Can be sync or async.
     * @returns A function that unsubscribes the handler when called
     */
    on<K extends EventType>(eventType: K, handler: EventHandler<K>): () => void;
    on(eventType: "*", handler: WildcardEventHandler): () => void;
    /**
     * Emits an event to all registered handlers.
     * Automatically generates event fields (id, timestamp, parentId) and adds the event to the session history.
     * Triggers both legacy callbacks and new-style event handlers.
     *
     * @param eventType - The type of event to emit
     * @param data - The event data payload
     * @param ephemeral - Whether this event should be persisted (default: false)
     */
    private emitInternal;
    /**
     * Emits an event to all registered handlers.
     * Automatically generates event fields (id, timestamp, parentId) and adds the event to the session history.
     * Triggers both legacy callbacks and new-style event handlers. Does not allow emitting ephemeral events.
     *
     * @param eventType - The type of event to emit
     * @param data - The event data payload
     */
    emit<K extends EventType>(eventType: EventPayload<K>["ephemeral"] extends true ? never : K, data: EventData<K>): void;
    /**
     * Emits an ephemeral event (not persisted to disk).
     * Convenience method that calls emit() with ephemeral = true.
     *
     * @param eventType - The type of event to emit
     * @param data - The event data payload
     */
    emitEphemeral<K extends EventType>(eventType: EventPayload<K>["ephemeral"] extends false ? never : K, data: EventData<K>): void;
    /**
     * Returns all events that have occurred in the session.
     * Events are returned in chronological order and include all session lifecycle, user messages,
     * assistant responses, tool executions, and other session events.
     *
     * @returns A readonly array of all session events
     */
    getEvents(): readonly SessionEvent[];
    /**
     * Truncates the session to a specific event, removing all events after it.
     * This clears and rebuilds the internal state (chat messages, etc.) from the remaining events.
     *
     * Note: This only affects in-memory state. The caller is responsible for
     * persisting the truncation to disk (e.g., via SessionEventState.truncate).
     *
     * @param upToEventId - The event ID to truncate to (this event is excluded)
     * @returns The number of events removed
     */
    truncateToEvent(upToEventId: string): Promise<{
        eventsRemoved: number;
    }>;
    /**
     * Returns all chat messages reconstructed from session events.
     * Messages are processed asynchronously in order to handle attachments and ensure consistency.
     * Includes user messages, assistant messages, tool messages, and system messages.
     *
     * @returns A Promise that resolves to a readonly array of chat completion messages
     * ```
     */
    getChatMessages(): Promise<readonly ChatCompletionMessageParam[]>;
    /**
     * Returns chat messages that are part of the conversation context (excludes system messages).
     * Useful for displaying the user/assistant conversation without system prompts.
     *
     * @returns A Promise that resolves to an array of user, assistant, and tool messages
     */
    getChatContextMessages(): Promise<ChatCompletionMessageParam[]>;
    /**
     * Returns only system and developer messages from the chat history.
     * Useful for inspecting the system prompts and instructions given to the model.
     *
     * @returns A Promise that resolves to an array of system/developer messages
     */
    getSystemContextMessages(): Promise<ChatCompletionMessageParam[]>;
    /**
     * Returns the list of skills that have been invoked in this session.
     * Used for restoring skill permissions on session resume and for compaction.
     *
     * @returns A readonly array of invoked skill information
     */
    getInvokedSkills(): readonly InvokedSkillInfo[];
    /**
     * Returns the current system message used in the most recent agent turn.
     * This is the full system prompt that was sent to the model.
     *
     * @returns The system message string, or undefined if not yet initialized
     */
    getCurrentSystemMessage(): string | undefined;
    /**
     * Returns lightweight tool metadata for token counting.
     * Used by /context command to calculate token usage.
     *
     * @returns The array of tool metadata, or undefined if not yet initialized
     */
    getCurrentToolMetadata(): ToolMetadata[] | undefined;
    /**
     * Returns the currently selected model for this session.
     * The model may change during a session if `setSelectedModel` is called.
     *
     * @returns A Promise that resolves to the model identifier string, or undefined if no model is set
     */
    getSelectedModel(): Promise<string | undefined>;
    /**
     * Returns the organization custom instructions configured for this session.
     * These are additional instructions provided by the organization.
     *
     * @returns The organization custom instructions string, or undefined if not set
     */
    getOrganizationCustomInstructions(): string | undefined;
    /**
     * Returns whether custom instructions should be skipped for this session.
     *
     * @returns True if custom instructions should be skipped, false otherwise
     */
    getSkipCustomInstructions(): boolean;
    /**
     * Returns the individual custom instruction sources discovered for this session.
     * Uses the git root and working directory to match the system prompt loading path.
     *
     * @returns Array of individual instruction sources
     */
    getInstructionSources(): Promise<InstructionSource[]>;
    /**
     * Returns the system message configuration for this session.
     * This can be used to replace or append to the default system message.
     *
     * @returns The system message config, or undefined if not set
     */
    getSystemMessageConfig(): SystemMessageConfig | undefined;
    /**
     * Sets the workspace context for infinite sessions.
     * This context is injected into the system prompt to give the agent
     * awareness of the workspace and its history.
     *
     * @param context - The workspace context info, or undefined to clear
     */
    setWorkspaceContext(context: WorkspaceContextInfo | undefined): void;
    /**
     * Gets the current workspace context, if any.
     */
    getWorkspaceContext(): WorkspaceContextInfo | undefined;
    /**
     * Returns the effective capabilities for this session.
     * Applies runtime overrides (e.g., askUserDisabled, runningInInteractiveMode) to the base capabilities.
     */
    protected getEffectiveCapabilities(): Set<SessionCapability>;
    /**
     * Get the current workspace, if any.
     * Only available for LocalSession when infinite sessions are enabled.
     */
    getWorkspace(): Workspace | null;
    /**
     * Check if workspace features are enabled for this session.
     */
    isWorkspaceEnabled(): boolean;
    /**
     * Get the workspace path for this session.
     * Returns null for base Session (non-local sessions don't have workspaces).
     */
    getWorkspacePath(): string | null;
    /**
     * Get the number of checkpoints in the workspace.
     */
    getCheckpointCount(): number;
    /**
     * Rename the session (set custom name).
     * Updates both in-memory workspace and persists to disk.
     */
    renameSession(_name: string): Promise<void>;
    /**
     * List checkpoints with their titles for context injection.
     */
    listCheckpointTitles(): Promise<{
        number: number;
        title: string;
        filename: string;
    }[]>;
    /**
     * Check if a plan.md file exists in the workspace.
     */
    hasPlan(): boolean;
    /**
     * Ensure workspace exists for this session.
     * Only available for LocalSession when infinite sessions are enabled.
     */
    ensureWorkspace(_context?: WorkspaceContext): Promise<Workspace>;
    /**
     * Sets the selected model for this session and emits a model change event.
     * This allows switching models mid-session, with the change tracked in the event history.
     *
     * @param model - The model identifier to switch to
     * @returns A Promise that resolves when the model change event has been emitted
     */
    setSelectedModel(model: string): Promise<void>;
    /**
     * Compacts the conversation history into a single summary message.
     * Used by the /compact slash command for manual compaction.
     *
     * @returns Promise that resolves with compaction results
     * @throws Error if compaction fails or is not supported
     */
    abstract compactHistory(): Promise<CompactionResult>;
    /**
     * Get the ID of the last event (for parentId chaining)
     */
    private getLastEventId;
    /**
     * Add a function to the event processing queue to ensure sequential processing
     * Returns a promise that resolves when the function has been processed
     * Ensures that state updates from events are processed in order
     */
    protected enqueueEventProcessing<T>(fn: () => T | PromiseLike<T>): Promise<T>;
    /**
     * Process event to update internal state (_chatMessages, _selectedModel, etc.)
     */
    private processEventForState;
    private loadCustomAgents;
    /**
     * Applies compaction to chat messages by splitting at the checkpoint and creating
     * post-compaction messages. Updates _chatMessages and returns the compacted and new messages.
     *
     * @param checkpointLength - The number of messages at the compaction checkpoint
     * @param summaryContent - The summary content from compaction
     * @returns The compacted messages and any new messages added after the checkpoint
     */
    protected applyCompactionToMessages(checkpointLength: number, summaryContent: string): {
        compacted: ChatCompletionMessageParam[];
        newMessages: ChatCompletionMessageParam[];
    };
    /**
     * Event types that are transient/ephemeral and can be safely evicted after
     * compaction. These events are not needed for state reconstruction (they
     * don't contribute to chat messages) and are the primary source of memory
     * growth in long-running sessions.
     */
    private static readonly TRANSIENT_EVENT_TYPES;
    /**
     * Evicts transient events from the events array for events that occurred
     * before the given index. This reduces memory without affecting state
     * reconstruction or timeline display for structural events.
     */
    private evictTransientEventsBeforeIndex;
}

/** Overall schema for session API. This is the entrypoint for generating the session API JSON schema that is used in the SDK. */
declare const sessionApiSchema: {
    model: {
        getCurrent: {
            result: ZodObject<    {
            modelId: ZodOptional<ZodString>;
            }, "strip", ZodTypeAny, {
            modelId?: string | undefined;
            }, {
            modelId?: string | undefined;
            }>;
        };
        switchTo: {
            params: ZodObject<    {
            modelId: ZodString;
            }, "strip", ZodTypeAny, {
            modelId: string;
            }, {
            modelId: string;
            }>;
            result: ZodObject<    {
            modelId: ZodOptional<ZodString>;
            }, "strip", ZodTypeAny, {
            modelId?: string | undefined;
            }, {
            modelId?: string | undefined;
            }>;
        };
    };
};

/**
 * # Session Capabilities
 *
 * Session capabilities control what prompt content, tools, and behaviors are available in a
 * given session. They prevent features intended for one client type from leaking into others —
 * for example, TUI keyboard shortcuts appearing in SDK prompts, or memory tools being offered
 * to headless clients that don't support them.
 *
 * ## Capability matrix
 *
 * | Capability              | CLI TUI | SDK | ACP | General Agent |
 * |-------------------------|---------|-----|-----|---------------|
 * | tui-hints               |    ✓    |     |     |               |
 * | plan-mode               |    ✓    |     |  ✓  |               |
 * | memory                  |    ✓    |     |     |       ✓       |
 * | cli-documentation       |    ✓    |     |     |       ✓       |
 * | ask-user                |    ✓    |  ✓  |     |       ✓       |
 * | interactive-mode        |    ✓    |  ✓  |  ✓  |               |
 * | system-notifications    |    ✓    |  ✓  |  ✓  |               |
 *
 * ## Where capabilities are checked
 *
 * 1. **Prompt generation** (`src/cli/prompts/system.ts`) — Conditional sections are included
 *    or excluded based on `hasCapability()`.
 * 2. **Tool creation** (`src/agents/config.ts`) — Tools are only added when their capability
 *    is active.
 *
 * ## Runtime overrides
 *
 * `Session.getEffectiveCapabilities()` can further narrow capabilities at runtime:
 * - `askUserDisabled` → removes `ask-user`
 * - `runningInInteractiveMode === false` → removes `interactive-mode`
 *
 * ## When to add a new capability vs reuse an existing one
 *
 * **Add a new capability when:**
 * - You're introducing a tool or prompt section only relevant to a subset of client types
 * - No existing capability's client set matches who needs this feature
 *
 * **Reuse an existing capability when:**
 * - Your feature naturally belongs to the same audience (e.g., a new keyboard shortcut → `tui-hints`)
 * - The feature would always be enabled/disabled together with an existing one
 *
 * **Guiding principle:** A capability represents a _category of functionality for a type of
 * client_, not an individual feature. Keep the list small.
 *
 * ## What capabilities are NOT for
 *
 * - **Feature flags**: Use `FeatureFlagManager` for gradual rollouts. Capabilities are static per client type.
 * - **Model capabilities**: `SweBenchAgentCapabilities` (e.g., `parallel_tool_calls`) describes model support.
 * - **Runtime modes**: Concepts like autopilot that are purely behavioral toggles don't need capabilities.
 *
 * ## Adding a new capability
 *
 * 1. Add the literal to `SessionCapability` below
 * 2. Add it to `ALL_SESSION_CAPABILITIES`
 * 3. Add it to the appropriate client sets (`SDK_CAPABILITIES`, `ACP_CAPABILITIES`, etc.)
 * 4. Use `hasCapability(caps, "your-capability")` in prompt generation and/or tool creation
 * 5. Add behavioral tests in `test/core/sessionCapabilities.test.ts`
 * 6. Update snapshots: `npm run test:update-snapshots`
 */
declare type SessionCapability = "tui-hints" | "plan-mode" | "memory" | "cli-documentation" | "ask-user" | "interactive-mode" | "system-notifications";

export declare type SessionCompactionCompleteData = SessionCompactionCompleteEvent["data"];

export declare type SessionCompactionCompleteEvent = z_2.infer<typeof SessionCompactionCompleteEventSchema>;

/**
 * Session compaction complete event - conversation history compaction finished (success or failure)
 */
declare const SessionCompactionCompleteEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.compaction_complete">;
    data: z_2.ZodObject<{
        success: z_2.ZodBoolean;
        error: z_2.ZodOptional<z_2.ZodString>;
        preCompactionTokens: z_2.ZodOptional<z_2.ZodNumber>;
        postCompactionTokens: z_2.ZodOptional<z_2.ZodNumber>;
        preCompactionMessagesLength: z_2.ZodOptional<z_2.ZodNumber>;
        messagesRemoved: z_2.ZodOptional<z_2.ZodNumber>;
        tokensRemoved: z_2.ZodOptional<z_2.ZodNumber>;
        summaryContent: z_2.ZodOptional<z_2.ZodString>;
        checkpointNumber: z_2.ZodOptional<z_2.ZodNumber>;
        checkpointPath: z_2.ZodOptional<z_2.ZodString>;
        compactionTokensUsed: z_2.ZodOptional<z_2.ZodObject<{
            input: z_2.ZodNumber;
            output: z_2.ZodNumber;
            cachedInput: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            input: number;
            output: number;
            cachedInput: number;
        }, {
            input: number;
            output: number;
            cachedInput: number;
        }>>;
        requestId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    }, {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    };
    id: string;
    type: "session.compaction_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    };
    id: string;
    type: "session.compaction_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionCompactionStartEvent = z_2.infer<typeof SessionCompactionStartEventSchema>;

/**
 * Session compaction started event - conversation history compaction has begun
 */
declare const SessionCompactionStartEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.compaction_start">;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    type: "session.compaction_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {};
    id: string;
    type: "session.compaction_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

/**
 * Working directory context for session tracking
 */
export declare interface SessionContext {
    readonly cwd: string;
    readonly gitRoot?: string;
    readonly repository?: string;
    readonly branch?: string;
}

export declare type SessionContextChangedEvent = z_2.infer<typeof SessionContextChangedEventSchema>;

/**
 * Emitted when the session's working directory context changes (e.g., branch switch, cwd change).
 */
declare const SessionContextChangedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.context_changed">;
    data: z_2.ZodObject<{
        cwd: z_2.ZodString;
        gitRoot: z_2.ZodOptional<z_2.ZodString>;
        repository: z_2.ZodOptional<z_2.ZodString>;
        branch: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    }, {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    };
    id: string;
    type: "session.context_changed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    };
    id: string;
    type: "session.context_changed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionEndHook = (input: SessionEndHookInput) => Promise<SessionEndHookOutput | void>;

/**
 * Session end hook types
 */
export declare interface SessionEndHookInput extends BaseHookInput {
    reason: "complete" | "error" | "abort" | "timeout" | "user_exit";
    finalMessage?: string;
    error?: Error;
}

export declare interface SessionEndHookOutput {
    suppressOutput?: boolean;
    cleanupActions?: string[];
    sessionSummary?: string;
}

export declare type SessionErrorEvent = z_2.infer<typeof SessionErrorEventSchema>;

/**
 * Error notification (for timeline/UI display)
 */
declare const SessionErrorEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.error">;
    data: z_2.ZodObject<{
        errorType: z_2.ZodString;
        message: z_2.ZodString;
        stack: z_2.ZodOptional<z_2.ZodString>;
        statusCode: z_2.ZodOptional<z_2.ZodNumber>;
        providerCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    }, {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    };
    id: string;
    type: "session.error";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    };
    id: string;
    type: "session.error";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

/**
 * Union of all session event types
 */
export declare type SessionEvent = z_2.infer<typeof SessionEventSchema>;

/**
 * Discriminated union of all event schemas
 */
export declare const SessionEventSchema: z_2.ZodDiscriminatedUnion<"type", [z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.start">;
    data: z_2.ZodObject<{
        sessionId: z_2.ZodString;
        version: z_2.ZodNumber;
        producer: z_2.ZodString;
        copilotVersion: z_2.ZodString;
        startTime: z_2.ZodString;
        selectedModel: z_2.ZodOptional<z_2.ZodString>;
        context: z_2.ZodOptional<z_2.ZodObject<{
            cwd: z_2.ZodString;
            gitRoot: z_2.ZodOptional<z_2.ZodString>;
            repository: z_2.ZodOptional<z_2.ZodString>;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    }, {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    };
    id: string;
    type: "session.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    };
    id: string;
    type: "session.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.resume">;
    data: z_2.ZodObject<{
        resumeTime: z_2.ZodString;
        eventCount: z_2.ZodNumber;
        context: z_2.ZodOptional<z_2.ZodObject<{
            cwd: z_2.ZodString;
            gitRoot: z_2.ZodOptional<z_2.ZodString>;
            repository: z_2.ZodOptional<z_2.ZodString>;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    }, {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    };
    id: string;
    type: "session.resume";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    };
    id: string;
    type: "session.resume";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.error">;
    data: z_2.ZodObject<{
        errorType: z_2.ZodString;
        message: z_2.ZodString;
        stack: z_2.ZodOptional<z_2.ZodString>;
        statusCode: z_2.ZodOptional<z_2.ZodNumber>;
        providerCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    }, {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    };
    id: string;
    type: "session.error";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        errorType: string;
        statusCode?: number | undefined;
        stack?: string | undefined;
        providerCallId?: string | undefined;
    };
    id: string;
    type: "session.error";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.idle">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    ephemeral: true;
    type: "session.idle";
    timestamp: string;
    parentId: string | null;
}, {
    data: {};
    id: string;
    ephemeral: true;
    type: "session.idle";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.title_changed">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        title: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        title: string;
    }, {
        title: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        title: string;
    };
    id: string;
    ephemeral: true;
    type: "session.title_changed";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        title: string;
    };
    id: string;
    ephemeral: true;
    type: "session.title_changed";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.info">;
    data: z_2.ZodObject<{
        infoType: z_2.ZodString;
        message: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        infoType: string;
    }, {
        message: string;
        infoType: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        infoType: string;
    };
    id: string;
    type: "session.info";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        infoType: string;
    };
    id: string;
    type: "session.info";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.warning">;
    data: z_2.ZodObject<{
        warningType: z_2.ZodString;
        message: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        warningType: string;
    }, {
        message: string;
        warningType: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        warningType: string;
    };
    id: string;
    type: "session.warning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        warningType: string;
    };
    id: string;
    type: "session.warning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.model_change">;
    data: z_2.ZodObject<{
        previousModel: z_2.ZodOptional<z_2.ZodString>;
        newModel: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        newModel: string;
        previousModel?: string | undefined;
    }, {
        newModel: string;
        previousModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        newModel: string;
        previousModel?: string | undefined;
    };
    id: string;
    type: "session.model_change";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        newModel: string;
        previousModel?: string | undefined;
    };
    id: string;
    type: "session.model_change";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.import_legacy">;
    data: z_2.ZodObject<{
        legacySession: z_2.ZodObject<{
            sessionId: z_2.ZodString;
            startTime: z_2.ZodDate;
            chatMessages: z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"developer">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"system">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"image_url">;
                    image_url: z_2.ZodObject<{
                        url: z_2.ZodString;
                        detail: z_2.ZodOptional<z_2.ZodEnum<["auto", "low", "high"]>>;
                    }, "strip", z_2.ZodTypeAny, {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    }, {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                }, {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"input_audio">;
                    input_audio: z_2.ZodObject<{
                        data: z_2.ZodString;
                        format: z_2.ZodUnion<[z_2.ZodLiteral<"wav">, z_2.ZodLiteral<"mp3">]>;
                    }, "strip", z_2.ZodTypeAny, {
                        data: string;
                        format: "wav" | "mp3";
                    }, {
                        data: string;
                        format: "wav" | "mp3";
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                }, {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"file">;
                    file: z_2.ZodObject<{
                        file_date: z_2.ZodOptional<z_2.ZodString>;
                        file_id: z_2.ZodOptional<z_2.ZodString>;
                        filename: z_2.ZodOptional<z_2.ZodString>;
                    }, "strip", z_2.ZodTypeAny, {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    }, {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                }, {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                }>]>, "many">]>;
                role: z_2.ZodLiteral<"user">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            }, {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"refusal">;
                    refusal: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    refusal: string;
                    type: "refusal";
                }, {
                    refusal: string;
                    type: "refusal";
                }>]>, "many">]>>>;
                role: z_2.ZodLiteral<"assistant">;
                name: z_2.ZodOptional<z_2.ZodString>;
                refusal: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodString>>;
                audio: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodObject<{
                    id: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    id: string;
                }, {
                    id: string;
                }>>>;
                function_call: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodObject<{
                    name: z_2.ZodString;
                    arguments: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    name: string;
                    arguments: string;
                }, {
                    name: string;
                    arguments: string;
                }>>>;
                tool_calls: z_2.ZodOptional<z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    id: z_2.ZodString;
                    type: z_2.ZodLiteral<"function">;
                    function: z_2.ZodObject<{
                        name: z_2.ZodString;
                        arguments: z_2.ZodString;
                    }, "strip", z_2.ZodTypeAny, {
                        name: string;
                        arguments: string;
                    }, {
                        name: string;
                        arguments: string;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                }, {
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                }>, z_2.ZodObject<{
                    id: z_2.ZodString;
                    type: z_2.ZodLiteral<"custom">;
                    custom: z_2.ZodObject<{
                        name: z_2.ZodString;
                        input: z_2.ZodString;
                    }, "strip", z_2.ZodTypeAny, {
                        input: string;
                        name: string;
                    }, {
                        input: string;
                        name: string;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                }, {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                }>]>, "many">>;
            }, "strip", z_2.ZodTypeAny, {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            }, {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"tool">;
                tool_call_id: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            }>, z_2.ZodObject<{
                content: z_2.ZodNullable<z_2.ZodString>;
                role: z_2.ZodLiteral<"function">;
                name: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                content: string | null;
                role: "function";
            }, {
                name: string;
                content: string | null;
                role: "function";
            }>]>, "many">;
            timeline: z_2.ZodArray<z_2.ZodIntersection<z_2.ZodUnion<[z_2.ZodObject<{
                type: z_2.ZodLiteral<"copilot">;
                text: z_2.ZodString;
                isStreaming: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            }, {
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"error">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "error";
                text: string;
            }, {
                type: "error";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"info">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "info";
                text: string;
            }, {
                type: "info";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"warning">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "warning";
                text: string;
            }, {
                type: "warning";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"user">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "user";
                text: string;
            }, {
                type: "user";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"tool_call_requested">;
                callId: z_2.ZodString;
                name: z_2.ZodString;
                toolTitle: z_2.ZodOptional<z_2.ZodString>;
                intentionSummary: z_2.ZodNullable<z_2.ZodString>;
                arguments: z_2.ZodUnion<[z_2.ZodUnion<[z_2.ZodObject<{
                    command: z_2.ZodString;
                    description: z_2.ZodString;
                    timeout: z_2.ZodOptional<z_2.ZodNumber>;
                    sessionId: z_2.ZodOptional<z_2.ZodString>;
                    async: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    input: z_2.ZodString;
                    delay: z_2.ZodOptional<z_2.ZodNumber>;
                }, "strip", z_2.ZodTypeAny, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    delay: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                    delay: number;
                }, {
                    sessionId: string;
                    delay: number;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                }, {
                    sessionId: string;
                }>]>, z_2.ZodDiscriminatedUnion<"command", [z_2.ZodObject<{
                    command: z_2.ZodLiteral<"view">;
                    path: z_2.ZodString;
                    view_range: z_2.ZodOptional<z_2.ZodTuple<[z_2.ZodNumber, z_2.ZodNumber], null>>;
                }, "strip", z_2.ZodTypeAny, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"create">;
                    path: z_2.ZodString;
                    file_text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "create";
                    path: string;
                    file_text: string;
                }, {
                    command: "create";
                    path: string;
                    file_text: string;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"str_replace">;
                    path: z_2.ZodString;
                    new_str: z_2.ZodOptional<z_2.ZodString>;
                    old_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"insert">;
                    path: z_2.ZodString;
                    insert_line: z_2.ZodNumber;
                    new_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }>]>, z_2.ZodUnknown]>;
                partialOutput: z_2.ZodOptional<z_2.ZodString>;
                isHidden: z_2.ZodOptional<z_2.ZodBoolean>;
                isAlwaysExpanded: z_2.ZodOptional<z_2.ZodBoolean>;
                showNoContent: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }, {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"tool_call_completed">;
                callId: z_2.ZodString;
                name: z_2.ZodString;
                toolTitle: z_2.ZodOptional<z_2.ZodString>;
                intentionSummary: z_2.ZodNullable<z_2.ZodString>;
                result: z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"success">;
                    log: z_2.ZodString;
                    detailedLog: z_2.ZodOptional<z_2.ZodString>;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                }, {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"failure">;
                    log: z_2.ZodString;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                }, {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"rejected">;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "rejected";
                    markdown?: boolean | undefined;
                }, {
                    type: "rejected";
                    markdown?: boolean | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"denied">;
                    log: z_2.ZodString;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                }, {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                }>]>;
                arguments: z_2.ZodUnion<[z_2.ZodUnion<[z_2.ZodObject<{
                    command: z_2.ZodString;
                    description: z_2.ZodString;
                    timeout: z_2.ZodOptional<z_2.ZodNumber>;
                    sessionId: z_2.ZodOptional<z_2.ZodString>;
                    async: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    input: z_2.ZodString;
                    delay: z_2.ZodOptional<z_2.ZodNumber>;
                }, "strip", z_2.ZodTypeAny, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    delay: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                    delay: number;
                }, {
                    sessionId: string;
                    delay: number;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                }, {
                    sessionId: string;
                }>]>, z_2.ZodDiscriminatedUnion<"command", [z_2.ZodObject<{
                    command: z_2.ZodLiteral<"view">;
                    path: z_2.ZodString;
                    view_range: z_2.ZodOptional<z_2.ZodTuple<[z_2.ZodNumber, z_2.ZodNumber], null>>;
                }, "strip", z_2.ZodTypeAny, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"create">;
                    path: z_2.ZodString;
                    file_text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "create";
                    path: string;
                    file_text: string;
                }, {
                    command: "create";
                    path: string;
                    file_text: string;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"str_replace">;
                    path: z_2.ZodString;
                    new_str: z_2.ZodOptional<z_2.ZodString>;
                    old_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"insert">;
                    path: z_2.ZodString;
                    insert_line: z_2.ZodNumber;
                    new_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }>]>, z_2.ZodUnknown]>;
                isHidden: z_2.ZodOptional<z_2.ZodBoolean>;
                isAlwaysExpanded: z_2.ZodOptional<z_2.ZodBoolean>;
                showNoContent: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }, {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }>]>, z_2.ZodObject<{
                id: z_2.ZodString;
                timestamp: z_2.ZodDate;
            }, "strip", z_2.ZodTypeAny, {
                id: string;
                timestamp: Date;
            }, {
                id: string;
                timestamp: Date;
            }>>, "many">;
            selectedModel: z_2.ZodOptional<z_2.ZodEnum<["claude-sonnet-4.5", "claude-haiku-4.5", "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.6-1m", "claude-opus-4.5", "claude-sonnet-4", "gemini-3-pro-preview", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1", "gpt-5", "gpt-5.1-codex-mini", "gpt-5-mini", "gpt-4.1"]>>;
        }, "strip", z_2.ZodTypeAny, {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        }, {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        }>;
        importTime: z_2.ZodString;
        sourceFile: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    }, {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    };
    id: string;
    type: "session.import_legacy";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    };
    id: string;
    type: "session.import_legacy";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.handoff">;
    data: z_2.ZodObject<{
        handoffTime: z_2.ZodString;
        sourceType: z_2.ZodEnum<["remote", "local"]>;
        repository: z_2.ZodOptional<z_2.ZodObject<{
            owner: z_2.ZodString;
            name: z_2.ZodString;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            name: string;
            owner: string;
            branch?: string | undefined;
        }, {
            name: string;
            owner: string;
            branch?: string | undefined;
        }>>;
        context: z_2.ZodOptional<z_2.ZodString>;
        summary: z_2.ZodOptional<z_2.ZodString>;
        remoteSessionId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    }, {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    };
    id: string;
    type: "session.handoff";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    };
    id: string;
    type: "session.handoff";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.truncation">;
    data: z_2.ZodObject<{
        tokenLimit: z_2.ZodNumber;
        preTruncationTokensInMessages: z_2.ZodNumber;
        preTruncationMessagesLength: z_2.ZodNumber;
        postTruncationTokensInMessages: z_2.ZodNumber;
        postTruncationMessagesLength: z_2.ZodNumber;
        tokensRemovedDuringTruncation: z_2.ZodNumber;
        messagesRemovedDuringTruncation: z_2.ZodNumber;
        performedBy: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    }, {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    };
    id: string;
    type: "session.truncation";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    };
    id: string;
    type: "session.truncation";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.snapshot_rewind">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        /** The event ID that was rewound to (events after this were removed) */
        upToEventId: z_2.ZodString;
        /** Number of events that were removed */
        eventsRemoved: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        eventsRemoved: number;
        upToEventId: string;
    }, {
        eventsRemoved: number;
        upToEventId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        eventsRemoved: number;
        upToEventId: string;
    };
    id: string;
    ephemeral: true;
    type: "session.snapshot_rewind";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        eventsRemoved: number;
        upToEventId: string;
    };
    id: string;
    ephemeral: true;
    type: "session.snapshot_rewind";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.shutdown">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        /** The type of shutdown: routine (normal exit) or error (crash/fatal error) */
        shutdownType: z_2.ZodEnum<["routine", "error"]>;
        /** Error reason if shutdownType is "error" */
        errorReason: z_2.ZodOptional<z_2.ZodString>;
        /** Total premium requests used in the session */
        totalPremiumRequests: z_2.ZodNumber;
        /** Total time spent in API calls (milliseconds) */
        totalApiDurationMs: z_2.ZodNumber;
        /** Session start time (Unix timestamp) */
        sessionStartTime: z_2.ZodNumber;
        /** Code changes made during the session */
        codeChanges: z_2.ZodObject<{
            linesAdded: z_2.ZodNumber;
            linesRemoved: z_2.ZodNumber;
            filesModified: z_2.ZodArray<z_2.ZodString, "many">;
        }, "strip", z_2.ZodTypeAny, {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        }, {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        }>;
        /** Per-model usage breakdown */
        modelMetrics: z_2.ZodRecord<z_2.ZodString, z_2.ZodObject<{
            requests: z_2.ZodObject<{
                count: z_2.ZodNumber;
                cost: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                count: number;
                cost: number;
            }, {
                count: number;
                cost: number;
            }>;
            usage: z_2.ZodObject<{
                inputTokens: z_2.ZodNumber;
                outputTokens: z_2.ZodNumber;
                cacheReadTokens: z_2.ZodNumber;
                cacheWriteTokens: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            }, {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            }>;
        }, "strip", z_2.ZodTypeAny, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>>;
        /** Currently selected model (if any) */
        currentModel: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    }, {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    };
    id: string;
    ephemeral: true;
    type: "session.shutdown";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    };
    id: string;
    ephemeral: true;
    type: "session.shutdown";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.context_changed">;
    data: z_2.ZodObject<{
        cwd: z_2.ZodString;
        gitRoot: z_2.ZodOptional<z_2.ZodString>;
        repository: z_2.ZodOptional<z_2.ZodString>;
        branch: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    }, {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    };
    id: string;
    type: "session.context_changed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        cwd: string;
        branch?: string | undefined;
        gitRoot?: string | undefined;
        repository?: string | undefined;
    };
    id: string;
    type: "session.context_changed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.usage_info">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        tokenLimit: z_2.ZodNumber;
        currentTokens: z_2.ZodNumber;
        messagesLength: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    }, {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    };
    id: string;
    ephemeral: true;
    type: "session.usage_info";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    };
    id: string;
    ephemeral: true;
    type: "session.usage_info";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.compaction_start">;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    type: "session.compaction_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {};
    id: string;
    type: "session.compaction_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.compaction_complete">;
    data: z_2.ZodObject<{
        success: z_2.ZodBoolean;
        error: z_2.ZodOptional<z_2.ZodString>;
        preCompactionTokens: z_2.ZodOptional<z_2.ZodNumber>;
        postCompactionTokens: z_2.ZodOptional<z_2.ZodNumber>;
        preCompactionMessagesLength: z_2.ZodOptional<z_2.ZodNumber>;
        messagesRemoved: z_2.ZodOptional<z_2.ZodNumber>;
        tokensRemoved: z_2.ZodOptional<z_2.ZodNumber>;
        summaryContent: z_2.ZodOptional<z_2.ZodString>;
        checkpointNumber: z_2.ZodOptional<z_2.ZodNumber>;
        checkpointPath: z_2.ZodOptional<z_2.ZodString>;
        compactionTokensUsed: z_2.ZodOptional<z_2.ZodObject<{
            input: z_2.ZodNumber;
            output: z_2.ZodNumber;
            cachedInput: z_2.ZodNumber;
        }, "strip", z_2.ZodTypeAny, {
            input: number;
            output: number;
            cachedInput: number;
        }, {
            input: number;
            output: number;
            cachedInput: number;
        }>>;
        requestId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    }, {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    };
    id: string;
    type: "session.compaction_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        success: boolean;
        error?: string | undefined;
        requestId?: string | undefined;
        preCompactionTokens?: number | undefined;
        postCompactionTokens?: number | undefined;
        preCompactionMessagesLength?: number | undefined;
        messagesRemoved?: number | undefined;
        tokensRemoved?: number | undefined;
        summaryContent?: string | undefined;
        checkpointNumber?: number | undefined;
        checkpointPath?: string | undefined;
        compactionTokensUsed?: {
            input: number;
            output: number;
            cachedInput: number;
        } | undefined;
    };
    id: string;
    type: "session.compaction_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"user.message">;
    data: z_2.ZodObject<{
        content: z_2.ZodString;
        transformedContent: z_2.ZodOptional<z_2.ZodString>;
        attachments: z_2.ZodOptional<z_2.ZodArray<z_2.ZodDiscriminatedUnion<"type", [z_2.ZodObject<{
            path: z_2.ZodString;
            displayName: z_2.ZodString;
            lineRange: z_2.ZodOptional<z_2.ZodObject<{
                start: z_2.ZodNumber;
                end: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                end: number;
                start: number;
            }, {
                end: number;
                start: number;
            }>>;
        } & {
            type: z_2.ZodLiteral<"file">;
        }, "strip", z_2.ZodTypeAny, {
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }, {
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }>, z_2.ZodObject<{
            path: z_2.ZodString;
            displayName: z_2.ZodString;
            lineRange: z_2.ZodOptional<z_2.ZodObject<{
                start: z_2.ZodNumber;
                end: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                end: number;
                start: number;
            }, {
                end: number;
                start: number;
            }>>;
        } & {
            type: z_2.ZodLiteral<"directory">;
        }, "strip", z_2.ZodTypeAny, {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }, {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }>, z_2.ZodObject<{
            type: z_2.ZodLiteral<"selection">;
            filePath: z_2.ZodString;
            displayName: z_2.ZodString;
            text: z_2.ZodString;
            selection: z_2.ZodObject<{
                start: z_2.ZodObject<{
                    line: z_2.ZodNumber;
                    character: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    line: number;
                    character: number;
                }, {
                    line: number;
                    character: number;
                }>;
                end: z_2.ZodObject<{
                    line: z_2.ZodNumber;
                    character: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    line: number;
                    character: number;
                }, {
                    line: number;
                    character: number;
                }>;
            }, "strip", z_2.ZodTypeAny, {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            }, {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            }>;
        }, "strip", z_2.ZodTypeAny, {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        }, {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        }>]>, "many">>;
        source: z_2.ZodOptional<z_2.ZodString>;
        /** The agent mode active when this message was sent */
        agentMode: z_2.ZodOptional<z_2.ZodEnum<["interactive", "plan", "autopilot", "shell"]>>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    }, {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    };
    id: string;
    type: "user.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    };
    id: string;
    type: "user.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"pending_messages.modified">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    ephemeral: true;
    type: "pending_messages.modified";
    timestamp: string;
    parentId: string | null;
}, {
    data: {};
    id: string;
    ephemeral: true;
    type: "pending_messages.modified";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.turn_start">;
    data: z_2.ZodObject<{
        turnId: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        turnId: string;
    }, {
        turnId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.intent">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        intent: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        intent: string;
    }, {
        intent: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        intent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.intent";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        intent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.intent";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.reasoning">;
    data: z_2.ZodObject<{
        reasoningId: z_2.ZodString;
        content: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        reasoningId: string;
    }, {
        content: string;
        reasoningId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        reasoningId: string;
    };
    id: string;
    type: "assistant.reasoning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        reasoningId: string;
    };
    id: string;
    type: "assistant.reasoning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.reasoning_delta">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        reasoningId: z_2.ZodString;
        deltaContent: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        reasoningId: string;
        deltaContent: string;
    }, {
        reasoningId: string;
        deltaContent: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        reasoningId: string;
        deltaContent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.reasoning_delta";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        reasoningId: string;
        deltaContent: string;
    };
    id: string;
    ephemeral: true;
    type: "assistant.reasoning_delta";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.message">;
    data: z_2.ZodObject<{
        messageId: z_2.ZodString;
        content: z_2.ZodString;
        toolRequests: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
            toolCallId: z_2.ZodString;
            name: z_2.ZodString;
            arguments: z_2.ZodUnknown;
            type: z_2.ZodOptional<z_2.ZodEnum<["function", "custom"]>>;
        }, "strip", z_2.ZodTypeAny, {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }, {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }>, "many">>;
        reasoningOpaque: z_2.ZodOptional<z_2.ZodString>;
        reasoningText: z_2.ZodOptional<z_2.ZodString>;
        encryptedContent: z_2.ZodOptional<z_2.ZodString>;
        /** Phase of generation for phased-output models (e.g. gpt-5.3-codex). */
        phase: z_2.ZodOptional<z_2.ZodString>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    }, {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "assistant.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        messageId: string;
        phase?: string | undefined;
        toolRequests?: {
            name: string;
            toolCallId: string;
            type?: "function" | "custom" | undefined;
            arguments?: unknown;
        }[] | undefined;
        reasoningOpaque?: string | undefined;
        reasoningText?: string | undefined;
        encryptedContent?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "assistant.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.message_delta">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        messageId: z_2.ZodString;
        deltaContent: z_2.ZodString;
        totalResponseSizeBytes: z_2.ZodOptional<z_2.ZodNumber>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    }, {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.message_delta";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        deltaContent: string;
        messageId: string;
        parentToolCallId?: string | undefined;
        totalResponseSizeBytes?: number | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.message_delta";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"assistant.turn_end">;
    data: z_2.ZodObject<{
        turnId: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        turnId: string;
    }, {
        turnId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        turnId: string;
    };
    id: string;
    type: "assistant.turn_end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"assistant.usage">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        model: z_2.ZodString;
        inputTokens: z_2.ZodOptional<z_2.ZodNumber>;
        outputTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cacheReadTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cacheWriteTokens: z_2.ZodOptional<z_2.ZodNumber>;
        cost: z_2.ZodOptional<z_2.ZodNumber>;
        duration: z_2.ZodOptional<z_2.ZodNumber>;
        initiator: z_2.ZodOptional<z_2.ZodString>;
        apiCallId: z_2.ZodOptional<z_2.ZodString>;
        providerCallId: z_2.ZodOptional<z_2.ZodString>;
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
        quotaSnapshots: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodObject<{
            isUnlimitedEntitlement: z_2.ZodBoolean;
            entitlementRequests: z_2.ZodNumber;
            usedRequests: z_2.ZodNumber;
            usageAllowedWithExhaustedQuota: z_2.ZodBoolean;
            overage: z_2.ZodNumber;
            overageAllowedWithExhaustedQuota: z_2.ZodBoolean;
            remainingPercentage: z_2.ZodNumber;
            resetDate: z_2.ZodOptional<z_2.ZodDate>;
        }, "strip", z_2.ZodTypeAny, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }>>>;
    }, "strip", z_2.ZodTypeAny, {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    }, {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.usage";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        model: string;
        duration?: number | undefined;
        inputTokens?: number | undefined;
        outputTokens?: number | undefined;
        providerCallId?: string | undefined;
        cost?: number | undefined;
        cacheReadTokens?: number | undefined;
        cacheWriteTokens?: number | undefined;
        parentToolCallId?: string | undefined;
        initiator?: string | undefined;
        apiCallId?: string | undefined;
        quotaSnapshots?: Record<string, {
            isUnlimitedEntitlement: boolean;
            entitlementRequests: number;
            usedRequests: number;
            usageAllowedWithExhaustedQuota: boolean;
            overage: number;
            overageAllowedWithExhaustedQuota: boolean;
            remainingPercentage: number;
            resetDate?: Date | undefined;
        }> | undefined;
    };
    id: string;
    ephemeral: true;
    type: "assistant.usage";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"abort">;
    data: z_2.ZodObject<{
        reason: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        reason: string;
    }, {
        reason: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        reason: string;
    };
    id: string;
    type: "abort";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        reason: string;
    };
    id: string;
    type: "abort";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.user_requested">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        toolName: z_2.ZodString;
        arguments: z_2.ZodUnknown;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    }, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    };
    id: string;
    type: "tool.user_requested";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    };
    id: string;
    type: "tool.user_requested";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.execution_start">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        toolName: z_2.ZodString;
        arguments: z_2.ZodUnknown;
        mcpServerName: z_2.ZodOptional<z_2.ZodString>;
        mcpToolName: z_2.ZodOptional<z_2.ZodString>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    }, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "tool.execution_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "tool.execution_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"tool.execution_partial_result">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        partialOutput: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        partialOutput: string;
    }, {
        toolCallId: string;
        partialOutput: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        partialOutput: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_partial_result";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        toolCallId: string;
        partialOutput: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_partial_result";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"tool.execution_progress">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        progressMessage: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        progressMessage: string;
    }, {
        toolCallId: string;
        progressMessage: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        progressMessage: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_progress";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        toolCallId: string;
        progressMessage: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_progress";
    timestamp: string;
    parentId: string | null;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.execution_complete">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        success: z_2.ZodBoolean;
        isUserRequested: z_2.ZodOptional<z_2.ZodBoolean>;
        result: z_2.ZodOptional<z_2.ZodObject<{
            /**
             * Tool result content sent to LLM for chat completion.
             * Typically concise/truncated for token efficiency.
             * Populated from: textResultForLlm || sessionLog
             */
            content: z_2.ZodString;
            /**
             * Detailed tool result for UI/timeline display.
             * Preserves full content like diffs. Optional - falls back to content.
             * Populated from: sessionLog || textResultForLlm
             */
            detailedContent: z_2.ZodOptional<z_2.ZodString>;
            /**
             * Structured content blocks from tool execution.
             * Contains rich content like text, images, audio, and resources in their native format.
             * Can be populated by any tool (MCP tools, bash, etc.) that returns structured content.
             */
            contents: z_2.ZodOptional<z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                type: z_2.ZodLiteral<"text">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "text";
                text: string;
            }, {
                type: "text";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"terminal">;
                text: z_2.ZodString;
                exitCode: z_2.ZodOptional<z_2.ZodNumber>;
                cwd: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            }, {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"image">;
                data: z_2.ZodString;
                mimeType: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                data: string;
                type: "image";
                mimeType: string;
            }, {
                data: string;
                type: "image";
                mimeType: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"audio">;
                data: z_2.ZodString;
                mimeType: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                data: string;
                type: "audio";
                mimeType: string;
            }, {
                data: string;
                type: "audio";
                mimeType: string;
            }>, z_2.ZodObject<{
                icons: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
                    src: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    sizes: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
                    theme: z_2.ZodOptional<z_2.ZodEnum<["light", "dark"]>>;
                }, "strip", z_2.ZodTypeAny, {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }, {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }>, "many">>;
                name: z_2.ZodString;
                title: z_2.ZodOptional<z_2.ZodString>;
                uri: z_2.ZodString;
                description: z_2.ZodOptional<z_2.ZodString>;
                mimeType: z_2.ZodOptional<z_2.ZodString>;
                size: z_2.ZodOptional<z_2.ZodNumber>;
            } & {
                type: z_2.ZodLiteral<"resource_link">;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            }, {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"resource">;
                resource: z_2.ZodUnion<[z_2.ZodObject<{
                    uri: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                }, {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                }>, z_2.ZodObject<{
                    uri: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    blob: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                }, {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                }>]>;
            }, "strip", z_2.ZodTypeAny, {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            }, {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            }>]>, "many">>;
        }, "strip", z_2.ZodTypeAny, {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        }, {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        }>>;
        error: z_2.ZodOptional<z_2.ZodObject<{
            message: z_2.ZodString;
            code: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            message: string;
            code?: string | undefined;
        }, {
            message: string;
            code?: string | undefined;
        }>>;
        toolTelemetry: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodUnknown>>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    }, {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    };
    id: string;
    type: "tool.execution_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    };
    id: string;
    type: "tool.execution_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"skill.invoked">;
    data: z_2.ZodObject<{
        /** The skill name */
        name: z_2.ZodString;
        /** Path to the SKILL.md file */
        path: z_2.ZodString;
        /** The full content of the skill file */
        content: z_2.ZodString;
        /** Tools that should be auto-approved when this skill is active */
        allowedTools: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
    }, "strip", z_2.ZodTypeAny, {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    }, {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    };
    id: string;
    type: "skill.invoked";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    };
    id: string;
    type: "skill.invoked";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.started">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        agentDescription: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    }, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    };
    id: string;
    type: "subagent.started";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    };
    id: string;
    type: "subagent.started";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.completed">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.completed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.completed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.failed">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        error: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }, {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.failed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.failed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.selected">;
    data: z_2.ZodObject<{
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        tools: z_2.ZodNullable<z_2.ZodArray<z_2.ZodString, "many">>;
    }, "strip", z_2.ZodTypeAny, {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    }, {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.selected";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.selected";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"hook.start">;
    data: z_2.ZodObject<{
        hookInvocationId: z_2.ZodString;
        hookType: z_2.ZodString;
        input: z_2.ZodUnknown;
    }, "strip", z_2.ZodTypeAny, {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    }, {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    };
    id: string;
    type: "hook.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        hookInvocationId: string;
        hookType: string;
        input?: unknown;
    };
    id: string;
    type: "hook.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"hook.end">;
    data: z_2.ZodObject<{
        hookInvocationId: z_2.ZodString;
        hookType: z_2.ZodString;
        output: z_2.ZodUnknown;
        success: z_2.ZodBoolean;
        error: z_2.ZodOptional<z_2.ZodObject<{
            message: z_2.ZodString;
            stack: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            message: string;
            stack?: string | undefined;
        }, {
            message: string;
            stack?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    }, {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    };
    id: string;
    type: "hook.end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        success: boolean;
        hookInvocationId: string;
        hookType: string;
        error?: {
            message: string;
            stack?: string | undefined;
        } | undefined;
        output?: unknown;
    };
    id: string;
    type: "hook.end";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>, z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"system.message">;
    data: z_2.ZodObject<{
        content: z_2.ZodString;
        role: z_2.ZodEnum<["system", "developer"]>;
        name: z_2.ZodOptional<z_2.ZodString>;
        metadata: z_2.ZodOptional<z_2.ZodObject<{
            promptVersion: z_2.ZodOptional<z_2.ZodString>;
            variables: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodUnknown>>;
        }, "strip", z_2.ZodTypeAny, {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        }, {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    }, {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    };
    id: string;
    type: "system.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    };
    id: string;
    type: "system.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>]>;

/**
 * All possible session event type strings
 */
export declare type SessionEventType = SessionEvent["type"];

export declare type SessionHandoffEvent = z_2.infer<typeof SessionHandoffEventSchema>;

/**
 * Remote session handed off to local
 */
declare const SessionHandoffEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.handoff">;
    data: z_2.ZodObject<{
        handoffTime: z_2.ZodString;
        sourceType: z_2.ZodEnum<["remote", "local"]>;
        repository: z_2.ZodOptional<z_2.ZodObject<{
            owner: z_2.ZodString;
            name: z_2.ZodString;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            name: string;
            owner: string;
            branch?: string | undefined;
        }, {
            name: string;
            owner: string;
            branch?: string | undefined;
        }>>;
        context: z_2.ZodOptional<z_2.ZodString>;
        summary: z_2.ZodOptional<z_2.ZodString>;
        remoteSessionId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    }, {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    };
    id: string;
    type: "session.handoff";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        handoffTime: string;
        sourceType: "remote" | "local";
        summary?: string | undefined;
        context?: string | undefined;
        repository?: {
            name: string;
            owner: string;
            branch?: string | undefined;
        } | undefined;
        remoteSessionId?: string | undefined;
    };
    id: string;
    type: "session.handoff";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionIdleEvent = z_2.infer<typeof SessionIdleEventSchema>;

/**
 * Session initialization event (first line of JSONL file)
 */
declare const SessionIdleEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.idle">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{}, "strip", z_2.ZodTypeAny, {}, {}>;
}, "strip", z_2.ZodTypeAny, {
    data: {};
    id: string;
    ephemeral: true;
    type: "session.idle";
    timestamp: string;
    parentId: string | null;
}, {
    data: {};
    id: string;
    ephemeral: true;
    type: "session.idle";
    timestamp: string;
    parentId: string | null;
}>;

export declare type SessionImportLegacyEvent = z_2.infer<typeof SessionImportLegacyEventSchema>;

/**
 * Legacy session imported (wraps entire legacy JSON)
 */
declare const SessionImportLegacyEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.import_legacy">;
    data: z_2.ZodObject<{
        legacySession: z_2.ZodObject<{
            sessionId: z_2.ZodString;
            startTime: z_2.ZodDate;
            chatMessages: z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"developer">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"system">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"image_url">;
                    image_url: z_2.ZodObject<{
                        url: z_2.ZodString;
                        detail: z_2.ZodOptional<z_2.ZodEnum<["auto", "low", "high"]>>;
                    }, "strip", z_2.ZodTypeAny, {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    }, {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                }, {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"input_audio">;
                    input_audio: z_2.ZodObject<{
                        data: z_2.ZodString;
                        format: z_2.ZodUnion<[z_2.ZodLiteral<"wav">, z_2.ZodLiteral<"mp3">]>;
                    }, "strip", z_2.ZodTypeAny, {
                        data: string;
                        format: "wav" | "mp3";
                    }, {
                        data: string;
                        format: "wav" | "mp3";
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                }, {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"file">;
                    file: z_2.ZodObject<{
                        file_date: z_2.ZodOptional<z_2.ZodString>;
                        file_id: z_2.ZodOptional<z_2.ZodString>;
                        filename: z_2.ZodOptional<z_2.ZodString>;
                    }, "strip", z_2.ZodTypeAny, {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    }, {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                }, {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                }>]>, "many">]>;
                role: z_2.ZodLiteral<"user">;
                name: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            }, {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"refusal">;
                    refusal: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    refusal: string;
                    type: "refusal";
                }, {
                    refusal: string;
                    type: "refusal";
                }>]>, "many">]>>>;
                role: z_2.ZodLiteral<"assistant">;
                name: z_2.ZodOptional<z_2.ZodString>;
                refusal: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodString>>;
                audio: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodObject<{
                    id: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    id: string;
                }, {
                    id: string;
                }>>>;
                function_call: z_2.ZodOptional<z_2.ZodNullable<z_2.ZodObject<{
                    name: z_2.ZodString;
                    arguments: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    name: string;
                    arguments: string;
                }, {
                    name: string;
                    arguments: string;
                }>>>;
                tool_calls: z_2.ZodOptional<z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                    id: z_2.ZodString;
                    type: z_2.ZodLiteral<"function">;
                    function: z_2.ZodObject<{
                        name: z_2.ZodString;
                        arguments: z_2.ZodString;
                    }, "strip", z_2.ZodTypeAny, {
                        name: string;
                        arguments: string;
                    }, {
                        name: string;
                        arguments: string;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                }, {
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                }>, z_2.ZodObject<{
                    id: z_2.ZodString;
                    type: z_2.ZodLiteral<"custom">;
                    custom: z_2.ZodObject<{
                        name: z_2.ZodString;
                        input: z_2.ZodString;
                    }, "strip", z_2.ZodTypeAny, {
                        input: string;
                        name: string;
                    }, {
                        input: string;
                        name: string;
                    }>;
                }, "strip", z_2.ZodTypeAny, {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                }, {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                }>]>, "many">>;
            }, "strip", z_2.ZodTypeAny, {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            }, {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            }>, z_2.ZodObject<{
                content: z_2.ZodUnion<[z_2.ZodString, z_2.ZodArray<z_2.ZodObject<{
                    type: z_2.ZodLiteral<"text">;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    type: "text";
                    text: string;
                }, {
                    type: "text";
                    text: string;
                }>, "many">]>;
                role: z_2.ZodLiteral<"tool">;
                tool_call_id: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            }, {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            }>, z_2.ZodObject<{
                content: z_2.ZodNullable<z_2.ZodString>;
                role: z_2.ZodLiteral<"function">;
                name: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                content: string | null;
                role: "function";
            }, {
                name: string;
                content: string | null;
                role: "function";
            }>]>, "many">;
            timeline: z_2.ZodArray<z_2.ZodIntersection<z_2.ZodUnion<[z_2.ZodObject<{
                type: z_2.ZodLiteral<"copilot">;
                text: z_2.ZodString;
                isStreaming: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            }, {
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"error">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "error";
                text: string;
            }, {
                type: "error";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"info">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "info";
                text: string;
            }, {
                type: "info";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"warning">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "warning";
                text: string;
            }, {
                type: "warning";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"user">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "user";
                text: string;
            }, {
                type: "user";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"tool_call_requested">;
                callId: z_2.ZodString;
                name: z_2.ZodString;
                toolTitle: z_2.ZodOptional<z_2.ZodString>;
                intentionSummary: z_2.ZodNullable<z_2.ZodString>;
                arguments: z_2.ZodUnion<[z_2.ZodUnion<[z_2.ZodObject<{
                    command: z_2.ZodString;
                    description: z_2.ZodString;
                    timeout: z_2.ZodOptional<z_2.ZodNumber>;
                    sessionId: z_2.ZodOptional<z_2.ZodString>;
                    async: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    input: z_2.ZodString;
                    delay: z_2.ZodOptional<z_2.ZodNumber>;
                }, "strip", z_2.ZodTypeAny, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    delay: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                    delay: number;
                }, {
                    sessionId: string;
                    delay: number;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                }, {
                    sessionId: string;
                }>]>, z_2.ZodDiscriminatedUnion<"command", [z_2.ZodObject<{
                    command: z_2.ZodLiteral<"view">;
                    path: z_2.ZodString;
                    view_range: z_2.ZodOptional<z_2.ZodTuple<[z_2.ZodNumber, z_2.ZodNumber], null>>;
                }, "strip", z_2.ZodTypeAny, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"create">;
                    path: z_2.ZodString;
                    file_text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "create";
                    path: string;
                    file_text: string;
                }, {
                    command: "create";
                    path: string;
                    file_text: string;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"str_replace">;
                    path: z_2.ZodString;
                    new_str: z_2.ZodOptional<z_2.ZodString>;
                    old_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"insert">;
                    path: z_2.ZodString;
                    insert_line: z_2.ZodNumber;
                    new_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }>]>, z_2.ZodUnknown]>;
                partialOutput: z_2.ZodOptional<z_2.ZodString>;
                isHidden: z_2.ZodOptional<z_2.ZodBoolean>;
                isAlwaysExpanded: z_2.ZodOptional<z_2.ZodBoolean>;
                showNoContent: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }, {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"tool_call_completed">;
                callId: z_2.ZodString;
                name: z_2.ZodString;
                toolTitle: z_2.ZodOptional<z_2.ZodString>;
                intentionSummary: z_2.ZodNullable<z_2.ZodString>;
                result: z_2.ZodUnion<[z_2.ZodObject<{
                    type: z_2.ZodLiteral<"success">;
                    log: z_2.ZodString;
                    detailedLog: z_2.ZodOptional<z_2.ZodString>;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                }, {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"failure">;
                    log: z_2.ZodString;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                }, {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"rejected">;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    type: "rejected";
                    markdown?: boolean | undefined;
                }, {
                    type: "rejected";
                    markdown?: boolean | undefined;
                }>, z_2.ZodObject<{
                    type: z_2.ZodLiteral<"denied">;
                    log: z_2.ZodString;
                    markdown: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                }, {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                }>]>;
                arguments: z_2.ZodUnion<[z_2.ZodUnion<[z_2.ZodObject<{
                    command: z_2.ZodString;
                    description: z_2.ZodString;
                    timeout: z_2.ZodOptional<z_2.ZodNumber>;
                    sessionId: z_2.ZodOptional<z_2.ZodString>;
                    async: z_2.ZodOptional<z_2.ZodBoolean>;
                }, "strip", z_2.ZodTypeAny, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }, {
                    command: string;
                    description: string;
                    timeout?: number | undefined;
                    sessionId?: string | undefined;
                    async?: boolean | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    input: z_2.ZodString;
                    delay: z_2.ZodOptional<z_2.ZodNumber>;
                }, "strip", z_2.ZodTypeAny, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }, {
                    input: string;
                    sessionId: string;
                    delay?: number | undefined;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                    delay: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                    delay: number;
                }, {
                    sessionId: string;
                    delay: number;
                }>, z_2.ZodObject<{
                    sessionId: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    sessionId: string;
                }, {
                    sessionId: string;
                }>]>, z_2.ZodDiscriminatedUnion<"command", [z_2.ZodObject<{
                    command: z_2.ZodLiteral<"view">;
                    path: z_2.ZodString;
                    view_range: z_2.ZodOptional<z_2.ZodTuple<[z_2.ZodNumber, z_2.ZodNumber], null>>;
                }, "strip", z_2.ZodTypeAny, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }, {
                    command: "view";
                    path: string;
                    view_range?: [number, number] | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"create">;
                    path: z_2.ZodString;
                    file_text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "create";
                    path: string;
                    file_text: string;
                }, {
                    command: "create";
                    path: string;
                    file_text: string;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"str_replace">;
                    path: z_2.ZodString;
                    new_str: z_2.ZodOptional<z_2.ZodString>;
                    old_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }, {
                    command: "str_replace";
                    path: string;
                    old_str: string;
                    new_str?: string | undefined;
                }>, z_2.ZodObject<{
                    command: z_2.ZodLiteral<"insert">;
                    path: z_2.ZodString;
                    insert_line: z_2.ZodNumber;
                    new_str: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }, {
                    command: "insert";
                    path: string;
                    new_str: string;
                    insert_line: number;
                }>]>, z_2.ZodUnknown]>;
                isHidden: z_2.ZodOptional<z_2.ZodBoolean>;
                isAlwaysExpanded: z_2.ZodOptional<z_2.ZodBoolean>;
                showNoContent: z_2.ZodOptional<z_2.ZodBoolean>;
            }, "strip", z_2.ZodTypeAny, {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }, {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }>]>, z_2.ZodObject<{
                id: z_2.ZodString;
                timestamp: z_2.ZodDate;
            }, "strip", z_2.ZodTypeAny, {
                id: string;
                timestamp: Date;
            }, {
                id: string;
                timestamp: Date;
            }>>, "many">;
            selectedModel: z_2.ZodOptional<z_2.ZodEnum<["claude-sonnet-4.5", "claude-haiku-4.5", "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.6-1m", "claude-opus-4.5", "claude-sonnet-4", "gemini-3-pro-preview", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1", "gpt-5", "gpt-5.1-codex-mini", "gpt-5-mini", "gpt-4.1"]>>;
        }, "strip", z_2.ZodTypeAny, {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        }, {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        }>;
        importTime: z_2.ZodString;
        sourceFile: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    }, {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    };
    id: string;
    type: "session.import_legacy";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        legacySession: {
            sessionId: string;
            startTime: Date;
            chatMessages: ({
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "developer";
                name?: string | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "system";
                name?: string | undefined;
            } | {
                content: string | ({
                    type: "text";
                    text: string;
                } | {
                    type: "image_url";
                    image_url: {
                        url: string;
                        detail?: "auto" | "low" | "high" | undefined;
                    };
                } | {
                    type: "input_audio";
                    input_audio: {
                        data: string;
                        format: "wav" | "mp3";
                    };
                } | {
                    type: "file";
                    file: {
                        file_date?: string | undefined;
                        file_id?: string | undefined;
                        filename?: string | undefined;
                    };
                })[];
                role: "user";
                name?: string | undefined;
            } | {
                role: "assistant";
                name?: string | undefined;
                tool_calls?: ({
                    function: {
                        name: string;
                        arguments: string;
                    };
                    id: string;
                    type: "function";
                } | {
                    id: string;
                    custom: {
                        input: string;
                        name: string;
                    };
                    type: "custom";
                })[] | undefined;
                content?: string | ({
                    type: "text";
                    text: string;
                } | {
                    refusal: string;
                    type: "refusal";
                })[] | null | undefined;
                refusal?: string | null | undefined;
                audio?: {
                    id: string;
                } | null | undefined;
                function_call?: {
                    name: string;
                    arguments: string;
                } | null | undefined;
            } | {
                content: string | {
                    type: "text";
                    text: string;
                }[];
                role: "tool";
                tool_call_id: string;
            } | {
                name: string;
                content: string | null;
                role: "function";
            })[];
            timeline: (({
                type: "copilot";
                text: string;
                isStreaming?: boolean | undefined;
            } | {
                type: "error";
                text: string;
            } | {
                type: "info";
                text: string;
            } | {
                type: "warning";
                text: string;
            } | {
                type: "user";
                text: string;
            } | {
                name: string;
                type: "tool_call_requested";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                partialOutput?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            } | {
                result: {
                    log: string;
                    type: "success";
                    markdown?: boolean | undefined;
                    detailedLog?: string | undefined;
                } | {
                    log: string;
                    type: "failure";
                    markdown?: boolean | undefined;
                } | {
                    type: "rejected";
                    markdown?: boolean | undefined;
                } | {
                    log: string;
                    type: "denied";
                    markdown?: boolean | undefined;
                };
                name: string;
                type: "tool_call_completed";
                callId: string;
                intentionSummary: string | null;
                arguments?: unknown;
                toolTitle?: string | undefined;
                isHidden?: boolean | undefined;
                isAlwaysExpanded?: boolean | undefined;
                showNoContent?: boolean | undefined;
            }) & {
                id: string;
                timestamp: Date;
            })[];
            selectedModel?: "gpt-5" | "gpt-5-mini" | "gpt-4.1" | "claude-sonnet-4.5" | "claude-opus-4.5" | "claude-opus-4.6" | "claude-opus-4.6-1m" | "claude-opus-4.6-fast" | "claude-haiku-4.5" | "claude-sonnet-4" | "gemini-3-pro-preview" | "gpt-5.3-codex" | "gpt-5.2-codex" | "gpt-5.2" | "gpt-5.1-codex-max" | "gpt-5.1-codex" | "gpt-5.1" | "gpt-5.1-codex-mini" | undefined;
        };
        importTime: string;
        sourceFile: string;
    };
    id: string;
    type: "session.import_legacy";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionInfoEvent = z_2.infer<typeof SessionInfoEventSchema>;

/**
 * Informational message (for timeline/UI display)
 */
declare const SessionInfoEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.info">;
    data: z_2.ZodObject<{
        infoType: z_2.ZodString;
        message: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        infoType: string;
    }, {
        message: string;
        infoType: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        infoType: string;
    };
    id: string;
    type: "session.info";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        infoType: string;
    };
    id: string;
    type: "session.info";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare type SessionLogsContent = SessionsChatCompletionMessageParam | SessionsChatCompletionChunk;

declare type SessionLogsContents = SessionLogsContent[];

/**
 * SessionManager interface
 */
export declare interface SessionManager<TSessionMetadata extends SessionMetadata = SessionMetadata, TSession extends Session<TSessionMetadata> = Session<TSessionMetadata>> {
    createSession(sessionOptions: SessionOptions): Promise<TSession>;
    getSession(options: {
        sessionId: string;
    }): Promise<TSession | undefined>;
    getLastSession(): Promise<TSession | undefined>;
    getLastSessionId(): Promise<string | undefined>;
    listSessions(): Promise<TSessionMetadata[]>;
    saveSession(session: TSession): Promise<void>;
    deleteSession(sessionId: string): Promise<void>;
    closeSession(sessionId: string): Promise<void>;
}

/**
 * SessionManager options - same as SessionOptions but used for creating the manager
 */
export declare type SessionManagerOptions = {
    logger?: RunnerLogger;
    integrationId?: string;
};

export declare interface SessionMetadata {
    readonly sessionId: string;
    readonly startTime: Date;
    readonly modifiedTime: Date;
    readonly summary?: string;
    readonly isRemote: boolean;
    /** Most recent working directory context (from last start or resume) */
    readonly context?: SessionContext;
}

export declare type SessionModelChangeEvent = z_2.infer<typeof SessionModelChangeEventSchema>;

/**
 * Model selection changed mid-session
 */
declare const SessionModelChangeEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.model_change">;
    data: z_2.ZodObject<{
        previousModel: z_2.ZodOptional<z_2.ZodString>;
        newModel: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        newModel: string;
        previousModel?: string | undefined;
    }, {
        newModel: string;
        previousModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        newModel: string;
        previousModel?: string | undefined;
    };
    id: string;
    type: "session.model_change";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        newModel: string;
        previousModel?: string | undefined;
    };
    id: string;
    type: "session.model_change";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare interface SessionOptions extends Partial<SessionMetadata> {
    clientName?: string;
    model?: SupportedModel;
    integrationId?: string;
    /**
     * Reasoning effort level for models that support it.
     * Valid values: "low", "medium", "high", "xhigh"
     * Only applies to models where reasoning effort is supported.
     */
    reasoningEffort?: "low" | "medium" | "high" | "xhigh";
    /**
     * Custom API provider configuration (BYOK - Bring Your Own Key).
     * When set, bypasses Copilot API authentication and uses this provider instead.
     */
    provider?: ProviderConfig;
    featureFlags?: FeatureFlags;
    isExperimentalMode?: boolean;
    availableTools?: string[];
    excludedTools?: string[];
    executeToolsInParallel?: boolean;
    shellConfig?: ShellConfig;
    requestPermission?: (permissionRequest: PermissionRequest) => Promise<PermissionRequestResult>;
    skillDirectories?: string[];
    disabledSkills?: Set<string>;
    installedPlugins?: InstalledPlugin[];
    mcpServers?: Record<string, MCPServerConfig>;
    mcpHost?: McpHost;
    customAgents?: SweCustomAgent[];
    selectedCustomAgent?: SweCustomAgent;
    organizationCustomInstructions?: string;
    skipCustomInstructions?: boolean;
    /** Set of instruction source IDs to exclude from the system prompt */
    disabledInstructionSources?: ReadonlySet<string>;
    systemMessage?: SystemMessageConfig;
    hooks?: QueryHooks;
    externalToolDefinitions?: ExternalToolDefinition[];
    externalToolDispatcher?: ExternalToolDispatcher;
    trajectoryFile?: string;
    eventsLogDirectory?: string;
    /**
     * Path to the session transcript (events.jsonl file).
     * Used by hooks to access the full conversation transcript.
     */
    transcriptPath?: string;
    workingDirectory?: string;
    /**
     * Repository name for the session context.
     * Used for memory service scoping, code search, and other repository-aware features.
     * Format: "owner/repo" (e.g., "github/copilot-cli")
     */
    repositoryName?: string;
    authInfo?: AuthInfo;
    copilotUrl?: string;
    enableStreaming?: boolean;
    largeOutput?: LargeToolOutputConfig;
    /** Callback invoked when a file is created. Useful for refreshing file caches. */
    onFileCreated?: (path: string) => void;
    /** Callback invoked when a warning should be emitted to the timeline. */
    onWarning?: (message: string) => void;
    /** Callback to request user input from the UI. Enables ask_user tool in interactive mode. */
    requestUserInput?: (request: {
        question: string;
        choices?: string[];
        allowFreeform?: boolean;
    }) => Promise<{
        answer: string;
        wasFreeform: boolean;
    }>;
    /** Whether ask_user is explicitly disabled (autonomous mode). When true, system prompt encourages independent action. */
    askUserDisabled?: boolean;
    /** Callback invoked when task_complete tool is called (autopilot mode only). */
    onTaskComplete?: (summary?: string) => void;
    /** Whether the CLI is running in interactive mode. Defaults to true. When false, uses non-interactive identity and excludes plan mode instructions. */
    runningInInteractiveMode?: boolean;
    /**
     * Capabilities enabled for this session. Controls prompt content, tool availability, and behaviors.
     * If not specified, defaults to all capabilities (TUI mode).
     * @see SessionCapability for available capabilities
     */
    sessionCapabilities?: Set<SessionCapability>;
    /**
     * Custom configuration directory for the session.
     * When set, overrides the default Copilot config directory (~/.copilot or $XDG_CONFIG_HOME/.copilot).
     */
    configDir?: string;
    /**
     * Runtime settings for persistence and workspace resolution.
     * Used to resolve config/state paths consistently.
     */
    runtimeSettings?: RuntimeSettings;
    /**
     * Infinite session configuration for persistent workspaces and automatic compaction.
     * When enabled, sessions automatically manage context limits and persist state.
     * Can be set to `true` for defaults, `false` to disable, or a config object for fine-tuning.
     * @default { enabled: true }
     */
    infiniteSessions?: InfiniteSessionConfig | boolean;
    /**
     * Getter for the content exclusion service.
     * Returns undefined when the service is not initialized (e.g., before auth or when feature is disabled).
     * Uses a getter function so the service can be updated during the session lifecycle.
     */
    getContentExclusionService?: () => ContentExclusionService | undefined;
    /**
     * Client name to use for LSP sessions.
     */
    lspClientName?: string;
}

export declare type SessionResumeEvent = z_2.infer<typeof SessionResumeEventSchema>;

/**
 * Session resumed from disk
 */
declare const SessionResumeEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.resume">;
    data: z_2.ZodObject<{
        resumeTime: z_2.ZodString;
        eventCount: z_2.ZodNumber;
        context: z_2.ZodOptional<z_2.ZodObject<{
            cwd: z_2.ZodString;
            gitRoot: z_2.ZodOptional<z_2.ZodString>;
            repository: z_2.ZodOptional<z_2.ZodString>;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    }, {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    };
    id: string;
    type: "session.resume";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        resumeTime: string;
        eventCount: number;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
    };
    id: string;
    type: "session.resume";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare type SessionsChatCompletionChunk = CopilotChatCompletionChunk & AgentIdBag;

declare type SessionsChatCompletionMessageParam = (SessionsUserMessageParam | ChatCompletionToolMessageParam) & AgentIdBag;

export declare type SessionShutdownData = SessionShutdownEvent["data"];

export declare type SessionShutdownEvent = z_2.infer<typeof SessionShutdownEventSchema>;

/**
 * Session shutdown event - emitted when the session is shutting down.
 * Contains aggregated usage metrics for the entire session.
 * This is ephemeral and used to trigger telemetry emission.
 */
declare const SessionShutdownEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.shutdown">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        /** The type of shutdown: routine (normal exit) or error (crash/fatal error) */
        shutdownType: z_2.ZodEnum<["routine", "error"]>;
        /** Error reason if shutdownType is "error" */
        errorReason: z_2.ZodOptional<z_2.ZodString>;
        /** Total premium requests used in the session */
        totalPremiumRequests: z_2.ZodNumber;
        /** Total time spent in API calls (milliseconds) */
        totalApiDurationMs: z_2.ZodNumber;
        /** Session start time (Unix timestamp) */
        sessionStartTime: z_2.ZodNumber;
        /** Code changes made during the session */
        codeChanges: z_2.ZodObject<{
            linesAdded: z_2.ZodNumber;
            linesRemoved: z_2.ZodNumber;
            filesModified: z_2.ZodArray<z_2.ZodString, "many">;
        }, "strip", z_2.ZodTypeAny, {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        }, {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        }>;
        /** Per-model usage breakdown */
        modelMetrics: z_2.ZodRecord<z_2.ZodString, z_2.ZodObject<{
            requests: z_2.ZodObject<{
                count: z_2.ZodNumber;
                cost: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                count: number;
                cost: number;
            }, {
                count: number;
                cost: number;
            }>;
            usage: z_2.ZodObject<{
                inputTokens: z_2.ZodNumber;
                outputTokens: z_2.ZodNumber;
                cacheReadTokens: z_2.ZodNumber;
                cacheWriteTokens: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            }, {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            }>;
        }, "strip", z_2.ZodTypeAny, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>>;
        /** Currently selected model (if any) */
        currentModel: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    }, {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    };
    id: string;
    ephemeral: true;
    type: "session.shutdown";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        shutdownType: "error" | "routine";
        totalPremiumRequests: number;
        totalApiDurationMs: number;
        sessionStartTime: number;
        codeChanges: {
            linesAdded: number;
            linesRemoved: number;
            filesModified: string[];
        };
        modelMetrics: Record<string, {
            usage: {
                inputTokens: number;
                outputTokens: number;
                cacheReadTokens: number;
                cacheWriteTokens: number;
            };
            requests: {
                count: number;
                cost: number;
            };
        }>;
        errorReason?: string | undefined;
        currentModel?: string | undefined;
    };
    id: string;
    ephemeral: true;
    type: "session.shutdown";
    timestamp: string;
    parentId: string | null;
}>;

export declare type SessionSnapshotRewindEvent = z_2.infer<typeof SessionSnapshotRewindEventSchema>;

/**
 * Session rewind event - emitted when session events are rewound (e.g., during snapshot rollback)
 * This is ephemeral and used to signal the UI to reconstruct its state from the remaining events.
 */
declare const SessionSnapshotRewindEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.snapshot_rewind">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        /** The event ID that was rewound to (events after this were removed) */
        upToEventId: z_2.ZodString;
        /** Number of events that were removed */
        eventsRemoved: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        eventsRemoved: number;
        upToEventId: string;
    }, {
        eventsRemoved: number;
        upToEventId: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        eventsRemoved: number;
        upToEventId: string;
    };
    id: string;
    ephemeral: true;
    type: "session.snapshot_rewind";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        eventsRemoved: number;
        upToEventId: string;
    };
    id: string;
    ephemeral: true;
    type: "session.snapshot_rewind";
    timestamp: string;
    parentId: string | null;
}>;

export declare type SessionStartEvent = z_2.infer<typeof SessionStartEventSchema>;

/**
 * Session initialization event (first line of JSONL file)
 */
declare const SessionStartEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.start">;
    data: z_2.ZodObject<{
        sessionId: z_2.ZodString;
        version: z_2.ZodNumber;
        producer: z_2.ZodString;
        copilotVersion: z_2.ZodString;
        startTime: z_2.ZodString;
        selectedModel: z_2.ZodOptional<z_2.ZodString>;
        context: z_2.ZodOptional<z_2.ZodObject<{
            cwd: z_2.ZodString;
            gitRoot: z_2.ZodOptional<z_2.ZodString>;
            repository: z_2.ZodOptional<z_2.ZodString>;
            branch: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }, {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    }, {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    };
    id: string;
    type: "session.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        version: number;
        sessionId: string;
        producer: string;
        copilotVersion: string;
        startTime: string;
        context?: {
            cwd: string;
            branch?: string | undefined;
            gitRoot?: string | undefined;
            repository?: string | undefined;
        } | undefined;
        selectedModel?: string | undefined;
    };
    id: string;
    type: "session.start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionStartHook = (input: SessionStartHookInput) => Promise<SessionStartHookOutput | void>;

/**
 * Session start hook types
 */
export declare interface SessionStartHookInput extends BaseHookInput {
    source: "startup" | "resume" | "new";
    initialPrompt?: string;
}

export declare interface SessionStartHookOutput {
    additionalContext?: string;
    modifiedConfig?: Record<string, unknown>;
}

declare type SessionsUserMessageParam = ChatCompletionUserMessageParam & {
    /**
     * The component which was the source of the user message.
     * - `jit-instruction`: The message was injected by something which adds automated instructions for the agent
     * - `command-{id}`: The message was injected as a result of a command with the given id
     * - `string`: Some other source
     */
    source?: string;
};

/**
 * Long-lived telemetry service that subscribes to session events and sends telemetry.
 * Survives session changes — call {@link setSession} when the active session changes.
 */
declare class SessionTelemetry {
    private readonly telemetryService;
    private session?;
    private unsubscribeEvents?;
    private unsubscribeTools?;
    private legacyHandler?;
    private hasEmittedTermInfo;
    /** Tool call tracking for tool_call_executed telemetry */
    private toolCallInfoMap;
    private tools;
    private currentApiCallId;
    constructor(telemetryService: TelemetryService);
    /**
     * Bind (or re-bind) to a session. Cleans up subscriptions from any previous session,
     * then subscribes to the new session's events and wires the telemetry handler.
     */
    setSession(session: LocalSession): void;
    private detachSession;
    dispose(): void;
    /**
     * Send a telemetry event with a simplified interface.
     * This is the preferred way to send telemetry events.
     *
     * @param event - The telemetry event to send
     *
     * TODO: restrictedProperties are currently not sent anywhere.
     * In the future, they should be sent in a separate restricted telemetry event.
     */
    sendTelemetry(event: TelemetryEvent_2): void;
    /**
     * Send a cli.telemetry hydro event with pre-filled session_id.
     * Note: copilot_tracking_id and client are added by the TelemetryService implementation.
     */
    private sendHydroTelemetryEvent;
    /**
     * Send a cli.model_call hydro event with pre-filled session_id.
     * Note: copilot_tracking_id and client are added by the TelemetryService implementation.
     */
    sendHydroModelCallEvent(event: HydroModelCall): void;
    /**
     * Send a cli.tool_call hydro event with pre-filled session_id.
     * Note: copilot_tracking_id and client are added by the TelemetryService implementation.
     */
    private sendHydroToolCallEvent;
    private getPrefilledHydroFields;
    private handleEvent;
    private forwardEventToTelemetry;
    private handleSessionError;
    private emitTermInfoIfNeeded;
    private trackToolCallInfo;
    private handleToolCallExecuted;
}

export declare type SessionTitleChangedEvent = z_2.infer<typeof SessionTitleChangedEventSchema>;

/**
 * Emitted when the session's display title changes.
 * Fires on:
 * - Initial workspace load (resume)
 * - Automatic name generation
 * - Session rename (user-initiated)
 */
declare const SessionTitleChangedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.title_changed">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        title: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        title: string;
    }, {
        title: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        title: string;
    };
    id: string;
    ephemeral: true;
    type: "session.title_changed";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        title: string;
    };
    id: string;
    ephemeral: true;
    type: "session.title_changed";
    timestamp: string;
    parentId: string | null;
}>;

export declare type SessionTruncationEvent = z_2.infer<typeof SessionTruncationEventSchema>;

declare const SessionTruncationEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.truncation">;
    data: z_2.ZodObject<{
        tokenLimit: z_2.ZodNumber;
        preTruncationTokensInMessages: z_2.ZodNumber;
        preTruncationMessagesLength: z_2.ZodNumber;
        postTruncationTokensInMessages: z_2.ZodNumber;
        postTruncationMessagesLength: z_2.ZodNumber;
        tokensRemovedDuringTruncation: z_2.ZodNumber;
        messagesRemovedDuringTruncation: z_2.ZodNumber;
        performedBy: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    }, {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    };
    id: string;
    type: "session.truncation";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
        performedBy: string;
    };
    id: string;
    type: "session.truncation";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SessionUsageInfoEvent = z_2.infer<typeof SessionUsageInfoEventSchema>;

/**
 * Usage info event - emitted every turn to report current context window token usage.
 * Unlike truncation events, this is always emitted regardless of whether truncation occurred.
 */
declare const SessionUsageInfoEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"session.usage_info">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        tokenLimit: z_2.ZodNumber;
        currentTokens: z_2.ZodNumber;
        messagesLength: z_2.ZodNumber;
    }, "strip", z_2.ZodTypeAny, {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    }, {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    };
    id: string;
    ephemeral: true;
    type: "session.usage_info";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        tokenLimit: number;
        currentTokens: number;
        messagesLength: number;
    };
    id: string;
    ephemeral: true;
    type: "session.usage_info";
    timestamp: string;
    parentId: string | null;
}>;

export declare type SessionWarningEvent = z_2.infer<typeof SessionWarningEventSchema>;

/**
 * Warning notification (for timeline/UI display)
 */
declare const SessionWarningEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"session.warning">;
    data: z_2.ZodObject<{
        warningType: z_2.ZodString;
        message: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        message: string;
        warningType: string;
    }, {
        message: string;
        warningType: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        message: string;
        warningType: string;
    };
    id: string;
    type: "session.warning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        message: string;
        warningType: string;
    };
    id: string;
    type: "session.warning";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare class ShellConfig {
    /**
     * Used to vary behavior programmatically for different shell implementations.
     */
    readonly shellType: ShellType;
    /**
     * Used when describing the shell type to users or in prompts.
     */
    readonly displayName: string;
    /**
     * Name of a tool that invokes a shell command.
     */
    readonly shellToolName: string;
    /**
     * Name of a tool that reads output from a shell session.
     */
    readonly readShellToolName: string;
    /**
     * Name of a tool that sends input to a shell session.
     */
    readonly writeShellToolName: string;
    /**
     * Name of a tool that terminates a shell session.
     */
    readonly stopShellToolName: string;
    /**
     * Name of a tool that lists all active shell sessions.
     */
    readonly listShellsToolName: string;
    /**
     * Additional information to add to the tool description.
     */
    readonly descriptionLines: string[];
    /**
     * A function that assesses the safety of a script to be run in the shell.
     * The reason this exists as a pluggable abstraction is so that only the
     * CLI needs to take a compile-time dependency on tree-sitter, and CCA doesn't.
     */
    readonly assessScriptSafety: (script: string, onWarning?: (message: string) => void) => Promise<SafetyAssessment>;
    constructor(
    /**
     * Used to vary behavior programmatically for different shell implementations.
     */
    shellType: ShellType, 
    /**
     * Used when describing the shell type to users or in prompts.
     */
    displayName: string, 
    /**
     * Name of a tool that invokes a shell command.
     */
    shellToolName: string, 
    /**
     * Name of a tool that reads output from a shell session.
     */
    readShellToolName: string, 
    /**
     * Name of a tool that sends input to a shell session.
     */
    writeShellToolName: string, 
    /**
     * Name of a tool that terminates a shell session.
     */
    stopShellToolName: string, 
    /**
     * Name of a tool that lists all active shell sessions.
     */
    listShellsToolName: string, 
    /**
     * Additional information to add to the tool description.
     */
    descriptionLines: string[], 
    /**
     * A function that assesses the safety of a script to be run in the shell.
     * The reason this exists as a pluggable abstraction is so that only the
     * CLI needs to take a compile-time dependency on tree-sitter, and CCA doesn't.
     */
    assessScriptSafety?: (script: string, onWarning?: (message: string) => void) => Promise<SafetyAssessment>);
    withScriptSafetyAssessor(settings: RuntimeSettings | undefined, assessor: (settings: RuntimeSettings | undefined, shellType: ShellType, script: string, onWarning?: (message: string) => void) => Promise<SafetyAssessment>): ShellConfig;
    static readonly bash: ShellConfig;
    static readonly powerShell: ShellConfig;
}

/**
 * A permission request for executing shell commands.
 */
declare type ShellPermissionRequest = {
    readonly kind: "shell";
    /** The full command that the user is being asked to approve, e.g. `echo foo && find -exec ... && git push` */
    readonly fullCommandText: string;
    /** A concise summary of the user's intention, e.g. "Echo foo and find a file and then run git push" */
    readonly intention: string;
    /**
     * The commands that are being invoked in the shell invocation.
     *
     * As a special case, which might be better represented in the type system, if there were no parsed commands
     * e.g. `export VAR=value`, then this will have a single entry with identifier equal to the fullCommandText.
     */
    readonly commands: ReadonlyArray<Command>;
    /**
     * Possible file paths that the command might access.
     *
     * This is entirely heuristic, so it's pretty untrustworthy.
     */
    readonly possiblePaths: ReadonlyArray<PossiblePath>;
    /**
     * Possible URLs that the command might access.
     *
     * This is entirely heuristic, so it's pretty untrustworthy.
     */
    readonly possibleUrls: ReadonlyArray<PossibleUrl>;
    /**
     * Indicates whether any command in the script has redirection to write to a file.
     */
    readonly hasWriteFileRedirection: boolean;
    /**
     * If there are complicated constructs, then persistent approval is not supported.
     * e.g. `cat $(echo "foo")` should not be persistently approvable because it's hard
     * for the user to understand the implications.
     */
    readonly canOfferSessionApproval: boolean;
    /**
     * Optional warning message to display (e.g., when the shell parser is unavailable).
     */
    readonly warning?: string;
};

/**
 * A background shell task (detached shell session).
 */
export declare type ShellTask = {
    type: "shell";
    id: string;
    description: string;
    status: BackgroundTaskStatus;
    startedAt: number;
    completedAt?: number;
    command: string;
    logPath: string;
    pid?: number;
};

/**
 * Progress for a detached shell task, derived from log file output.
 */
export declare type ShellTaskProgress = {
    type: "shell";
    /** Last 10 lines of log file output */
    recentOutput: string;
    /** Process ID read from .pid file */
    pid?: number;
};

declare type ShellType = "bash" | "powershell";

export declare type SkillInvokedEvent = z_2.infer<typeof SkillInvokedEventSchema>;

/**
 * A skill was successfully invoked and loaded.
 * Used to track skills for preservation across compaction.
 */
declare const SkillInvokedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"skill.invoked">;
    data: z_2.ZodObject<{
        /** The skill name */
        name: z_2.ZodString;
        /** Path to the SKILL.md file */
        path: z_2.ZodString;
        /** The full content of the skill file */
        content: z_2.ZodString;
        /** Tools that should be auto-approved when this skill is active */
        allowedTools: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
    }, "strip", z_2.ZodTypeAny, {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    }, {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    };
    id: string;
    type: "skill.invoked";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        name: string;
        content: string;
        path: string;
        allowedTools?: string[] | undefined;
    };
    id: string;
    type: "skill.invoked";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare interface SSETransportConfig {
    type: "sse";
    url: string;
    headers?: Record<string, string>;
    authProvider?: OAuthClientProvider;
}

declare interface StdioTransportConfig {
    type: "stdio";
    command: string;
    args: string[];
    env: Record<string, string>;
    stderr?: Writable;
    cwd?: string;
}

/**
 * Simplified streaming chunk context containing only the essential delta information
 * needed by processors. This avoids the complexity of converting between different
 * API formats (e.g., Responses API to ChatCompletion chunks).
 *
 * @deprecated In the future, we will move to a model where the individual API clients emit Core Runtime events, instead of the current model where ChatCompletions are the defacto interface.
 * Please avoid adding new streaming chunk processors, and instead consider if now is the right time to fix the abstraction gap between the ChatCompletionsClient and the DerivedResponsesClient.
 * Talk to @jmoseley or @mrayermannmsft for more context.
 */
declare type StreamingChunkContext = {
    /**
     * The streaming ID of the message.
     */
    streamingId: string;
    /**
     * Text content delta from this chunk.
     */
    content?: string;
    /**
     * Reasoning content delta from this chunk (chain-of-thought summaries).
     */
    reasoningContent?: string;
    /**
     * Arguments delta for the report_intent tool call. Processors can accumulate
     * these deltas to extract the intent once the JSON is complete.
     */
    reportIntentArguments?: string;
    /**
     * Approximate byte size of this chunk, calculated from content and all tool call data.
     */
    size: number;
};

export declare type SubagentCompletedEvent = z_2.infer<typeof SubagentCompletedEventSchema>;

/**
 * Subagent execution completes successfully
 */
declare const SubagentCompletedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.completed">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.completed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.completed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SubagentFailedEvent = z_2.infer<typeof SubagentFailedEventSchema>;

/**
 * Subagent execution fails
 */
declare const SubagentFailedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.failed">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        error: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }, {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.failed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        error: string;
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.failed";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SubagentSelectedEvent = z_2.infer<typeof SubagentSelectedEventSchema>;

/**
 * Subagent selected for the session
 */
declare const SubagentSelectedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.selected">;
    data: z_2.ZodObject<{
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        tools: z_2.ZodNullable<z_2.ZodArray<z_2.ZodString, "many">>;
    }, "strip", z_2.ZodTypeAny, {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    }, {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.selected";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        tools: string[] | null;
        agentName: string;
        agentDisplayName: string;
    };
    id: string;
    type: "subagent.selected";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SubagentStartedEvent = z_2.infer<typeof SubagentStartedEventSchema>;

/**
 * Subagent execution begins
 */
declare const SubagentStartedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"subagent.started">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        agentName: z_2.ZodString;
        agentDisplayName: z_2.ZodString;
        agentDescription: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    }, {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    };
    id: string;
    type: "subagent.started";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        agentName: string;
        agentDisplayName: string;
        agentDescription: string;
    };
    id: string;
    type: "subagent.started";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type SubagentStopHook = (input: SubagentStopHookInput) => Promise<SubagentStopHookOutput | void>;

/**
 * Subagent stop hook types - fires when a subagent completes, before returning result to parent
 */
export declare interface SubagentStopHookInput extends BaseHookInput {
    sessionId: string;
    transcriptPath: string;
    agentName: string;
    agentDisplayName?: string;
    stopReason: "end_turn";
}

export declare interface SubagentStopHookOutput {
    /** If "block", the subagent will continue with another turn using the reason. Undefined means "allow". */
    decision?: "block" | "allow";
    reason?: string;
}

/** List of supported model IDs in order of precedence to be used as the default */
declare const SUPPORTED_MODELS: readonly ["claude-sonnet-4.5", "claude-haiku-4.5", "claude-opus-4.6", "claude-opus-4.6-fast", "claude-opus-4.6-1m", "claude-opus-4.5", "claude-sonnet-4", "gemini-3-pro-preview", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1", "gpt-5", "gpt-5.1-codex-mini", "gpt-5-mini", "gpt-4.1"];

declare type SupportedModel = (typeof SUPPORTED_MODELS)[number];

/**
 * Subset of src/types/clients/types.ts that is required to actually run
 * a custom agent.
 */
export declare type SweCustomAgent = {
    name: string;
    displayName: string;
    description: string;
    tools: string[] | null;
    prompt: () => Promise<string>;
    mcpServers?: Record<string, MCPServerConfig>;
    infer: boolean;
};

/**
 * Append mode: Use CLI foundation with optional appended content (default).
 */
declare interface SystemMessageAppendConfig {
    mode?: "append";
    /**
     * Additional instructions appended after SDK-managed sections.
     */
    content?: string;
}

/**
 * System message configuration for session creation.
 * - Append mode (default): SDK foundation + optional custom content
 * - Replace mode: Full control, caller provides entire system message
 */
declare type SystemMessageConfig = SystemMessageAppendConfig | SystemMessageReplaceConfig;

export declare type SystemMessageEvent = z_2.infer<typeof SystemMessageEventSchema>;

/**
 * System message/prompt
 */
declare const SystemMessageEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"system.message">;
    data: z_2.ZodObject<{
        content: z_2.ZodString;
        role: z_2.ZodEnum<["system", "developer"]>;
        name: z_2.ZodOptional<z_2.ZodString>;
        metadata: z_2.ZodOptional<z_2.ZodObject<{
            promptVersion: z_2.ZodOptional<z_2.ZodString>;
            variables: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodUnknown>>;
        }, "strip", z_2.ZodTypeAny, {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        }, {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        }>>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    }, {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    };
    id: string;
    type: "system.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        role: "developer" | "system";
        name?: string | undefined;
        metadata?: {
            promptVersion?: string | undefined;
            variables?: Record<string, unknown> | undefined;
        } | undefined;
    };
    id: string;
    type: "system.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

/**
 * Replace mode: Use caller-provided system message entirely.
 * Removes all SDK guardrails including security restrictions.
 */
declare interface SystemMessageReplaceConfig {
    mode: "replace";
    /**
     * Complete system message content.
     * Replaces the entire SDK-managed system message.
     */
    content: string;
}

/**
 * Telemetry emitted by the runtime contains properties and metrics. These are non-sensitive pieces
 * of information. There are also restricted properties that must be used to store sensitive information.
 */
declare type Telemetry = {
    /**
     * Telemetry properties can be used to store string props.
     * WARNING: Do not put sensitive data here. Use restrictedProperties for that.
     */
    properties: Record<string, string | undefined>;
    /**
     * Restricted telemetry properties must be used to store sensitive string props. These props will only be available on the restricted kusto topics.
     * Nonnullable so it is harder to overlook.
     */
    restrictedProperties: Record<string, string | undefined>;
    /**
     * The name of the telemetry event associated with the emitted runtime event.
     */
    metrics: Record<string, number | undefined>;
};

/**
 * Alternatively telemetry can be emitted by an event which just contains telemetry. This is that type.
 *
 * You can use this type with our without generics. The generics help you to enforce what properties/metrics are on your event
 * more precisely and safely.
 */
declare type TelemetryEvent<EventT = string, TelemetryT extends Telemetry = Telemetry> = {
    kind: "telemetry";
    telemetry: EventTelemetry<EventT, TelemetryT>;
};

/**
 * End user wrapper for telemetry events.
 * Used by application code that doesn't need to care which hydro table what information is sent to.
 */
declare interface TelemetryEvent_2 {
    /** Event type/kind (e.g., "session_shutdown", "tool_call_executed") */
    kind: string;
    /** Non-restricted properties (key-value pairs) */
    properties?: Record<string, string | undefined>;
    /** Restricted properties (may contain sensitive data like PII, file paths, ...)  */
    restrictedProperties?: Record<string, string | undefined>;
    /** Numeric metrics */
    metrics?: Record<string, number | undefined>;
    /** Reference to the model call that produced this event */
    modelCallId?: string;
}

declare type TelemetryMeasurements = {
    [key: string]: number;
};

declare type TelemetryProperties = {
    [key: string]: string;
};

declare abstract class TelemetryService {
    protected readonly authManager?: AuthManager | undefined;
    constructor(authManager?: AuthManager | undefined);
    abstract sendTelemetryEvent(eventName: string, properties?: TelemetryProperties, measurements?: TelemetryMeasurements, tags?: TelemetryTags): void;
    abstract sendHydroEvent(event: HydroEvent): void;
    abstract dispose(): Promise<void> | void;
}

declare type TelemetryTags = {
    [key: string]: string;
};

export declare type TerminalContent = z_2.infer<typeof TerminalContentSchema>;

/**
 * Terminal content block with optional exit code and cwd
 */
declare const TerminalContentSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"terminal">;
    text: z_2.ZodString;
    exitCode: z_2.ZodOptional<z_2.ZodNumber>;
    cwd: z_2.ZodOptional<z_2.ZodString>;
}, "strip", z_2.ZodTypeAny, {
    type: "terminal";
    text: string;
    exitCode?: number | undefined;
    cwd?: string | undefined;
}, {
    type: "terminal";
    text: string;
    exitCode?: number | undefined;
    cwd?: string | undefined;
}>;

export declare type TextContent = z_2.infer<typeof TextContentSchema>;

/**
 * Text content block
 */
declare const TextContentSchema: z_2.ZodObject<{
    type: z_2.ZodLiteral<"text">;
    text: z_2.ZodString;
}, "strip", z_2.ZodTypeAny, {
    type: "text";
    text: string;
}, {
    type: "text";
    text: string;
}>;

/** Represents a Token authentication information using in the SDK. */
declare type TokenAuthInfo = {
    readonly type: "token";
    readonly host: string;
    readonly token: string;
    readonly copilotUser?: CopilotUserResponse;
};

declare type Tool = {
    name: string;
    namespacedName: string;
    /** Original/display name for the MCP server (may contain "/" and/or "__"). */
    mcpServerName?: string;
    /** Raw MCP tool name as reported by the server. */
    mcpToolName?: string;
    /** Intended for UI and end-user contexts — optimized to be human-readable
     * and easily understood, even by those unfamiliar with domain-specific terminology.
     *
     * If not provided, the name should be used for display (except for Tool, where
     * annotations.title should be given precedence over using name, if present).
     */
    title: string;
    description: string;
    input_schema: ToolInputSchema;
    readOnly?: boolean;
    safeForTelemetry: {
        name: boolean;
        inputsNames: boolean;
    };
    filterMode?: ContentFilterMode;
};

declare type Tool_2<CallbackT extends ToolCallback = ToolCallback> = ToolMetadata & {
    /**
     * The type of the tool. Defaults to "function" if not specified.
     * - "function": Standard function tool with JSON Schema input
     * - "custom": Custom tool with grammar-based input format
     */
    type?: "function" | "custom";
    /**
     * The input format for custom tools. Only used when type is "custom".
     */
    format?: CustomToolInputFormat;
    /**
     * A human readable string summary of what this command intends to do if executed.
     *
     * If not set, no summarised intention should be assumed by the caller.
     */
    summariseIntention?: (input: unknown) => string;
    callback: CallbackT;
    shutdown?: ToolShutdown;
    /**
     * Whether or not information about this tool is safe to send to telemetry without obfuscation.
     * - If `true`/`false`, then it will be assumed that all such information is safe/unsafe.
     * - If an object, then safety is determined per property.
     */
    safeForTelemetry?: {
        name: boolean;
        inputsNames: boolean;
    } | true;
};

/**
 * @param input The input to the tool
 * @param options Options for the tool, includes the standard `ToolCallbackOptions` as well as any additional options that were set for the tool in settings.
 */
declare type ToolCallback = (input: unknown, options?: ToolCallbackOptions) => Promise<ToolResult>;

declare type ToolCallbackOptions<OptionsT = {
    [key: string]: unknown;
}> = {
    /**
     * The ID of the LLM tool call which initiated this tool invocation.
     */
    toolCallId: string;
    truncationOptions?: {
        /**
         * The number of tokens that the tool's response should ideally be limited to.
         */
        tokenLimit: number;
        /**
         * A function to count the number of tokens in a string.
         */
        countTokens: (input: string) => number;
    };
    /**
     * A client that the tool can use to make chat completion calls.
     */
    client?: Client_2;
    /**
     * Other options specific to the tool. Passed in from settings.
     */
    toolOptions?: OptionsT;
    /**
     * Global runtime settings.
     */
    settings: RuntimeSettings;
    /**
     * An optional AbortSignal to allow cancellation of tool execution.
     */
    abortSignal?: AbortSignal;
    /**
     * Options for handling large tool outputs.
     */
    largeOutputOptions?: LargeOutputOptions;
};

export declare type ToolExecutionCompleteEvent = z_2.infer<typeof ToolExecutionCompleteEventSchema>;

/**
 * Tool execution completes (success or error)
 */
declare const ToolExecutionCompleteEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.execution_complete">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        success: z_2.ZodBoolean;
        isUserRequested: z_2.ZodOptional<z_2.ZodBoolean>;
        result: z_2.ZodOptional<z_2.ZodObject<{
            /**
             * Tool result content sent to LLM for chat completion.
             * Typically concise/truncated for token efficiency.
             * Populated from: textResultForLlm || sessionLog
             */
            content: z_2.ZodString;
            /**
             * Detailed tool result for UI/timeline display.
             * Preserves full content like diffs. Optional - falls back to content.
             * Populated from: sessionLog || textResultForLlm
             */
            detailedContent: z_2.ZodOptional<z_2.ZodString>;
            /**
             * Structured content blocks from tool execution.
             * Contains rich content like text, images, audio, and resources in their native format.
             * Can be populated by any tool (MCP tools, bash, etc.) that returns structured content.
             */
            contents: z_2.ZodOptional<z_2.ZodArray<z_2.ZodUnion<[z_2.ZodObject<{
                type: z_2.ZodLiteral<"text">;
                text: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                type: "text";
                text: string;
            }, {
                type: "text";
                text: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"terminal">;
                text: z_2.ZodString;
                exitCode: z_2.ZodOptional<z_2.ZodNumber>;
                cwd: z_2.ZodOptional<z_2.ZodString>;
            }, "strip", z_2.ZodTypeAny, {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            }, {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"image">;
                data: z_2.ZodString;
                mimeType: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                data: string;
                type: "image";
                mimeType: string;
            }, {
                data: string;
                type: "image";
                mimeType: string;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"audio">;
                data: z_2.ZodString;
                mimeType: z_2.ZodString;
            }, "strip", z_2.ZodTypeAny, {
                data: string;
                type: "audio";
                mimeType: string;
            }, {
                data: string;
                type: "audio";
                mimeType: string;
            }>, z_2.ZodObject<{
                icons: z_2.ZodOptional<z_2.ZodArray<z_2.ZodObject<{
                    src: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    sizes: z_2.ZodOptional<z_2.ZodArray<z_2.ZodString, "many">>;
                    theme: z_2.ZodOptional<z_2.ZodEnum<["light", "dark"]>>;
                }, "strip", z_2.ZodTypeAny, {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }, {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }>, "many">>;
                name: z_2.ZodString;
                title: z_2.ZodOptional<z_2.ZodString>;
                uri: z_2.ZodString;
                description: z_2.ZodOptional<z_2.ZodString>;
                mimeType: z_2.ZodOptional<z_2.ZodString>;
                size: z_2.ZodOptional<z_2.ZodNumber>;
            } & {
                type: z_2.ZodLiteral<"resource_link">;
            }, "strip", z_2.ZodTypeAny, {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            }, {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            }>, z_2.ZodObject<{
                type: z_2.ZodLiteral<"resource">;
                resource: z_2.ZodUnion<[z_2.ZodObject<{
                    uri: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    text: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                }, {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                }>, z_2.ZodObject<{
                    uri: z_2.ZodString;
                    mimeType: z_2.ZodOptional<z_2.ZodString>;
                    blob: z_2.ZodString;
                }, "strip", z_2.ZodTypeAny, {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                }, {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                }>]>;
            }, "strip", z_2.ZodTypeAny, {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            }, {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            }>]>, "many">>;
        }, "strip", z_2.ZodTypeAny, {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        }, {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        }>>;
        error: z_2.ZodOptional<z_2.ZodObject<{
            message: z_2.ZodString;
            code: z_2.ZodOptional<z_2.ZodString>;
        }, "strip", z_2.ZodTypeAny, {
            message: string;
            code?: string | undefined;
        }, {
            message: string;
            code?: string | undefined;
        }>>;
        toolTelemetry: z_2.ZodOptional<z_2.ZodRecord<z_2.ZodString, z_2.ZodUnknown>>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    }, {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    };
    id: string;
    type: "tool.execution_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        success: boolean;
        result?: {
            content: string;
            contents?: ({
                type: "text";
                text: string;
            } | {
                type: "terminal";
                text: string;
                exitCode?: number | undefined;
                cwd?: string | undefined;
            } | {
                data: string;
                type: "image";
                mimeType: string;
            } | {
                data: string;
                type: "audio";
                mimeType: string;
            } | {
                name: string;
                type: "resource_link";
                uri: string;
                mimeType?: string | undefined;
                size?: number | undefined;
                description?: string | undefined;
                title?: string | undefined;
                icons?: {
                    src: string;
                    mimeType?: string | undefined;
                    theme?: "light" | "dark" | undefined;
                    sizes?: string[] | undefined;
                }[] | undefined;
            } | {
                type: "resource";
                resource: {
                    text: string;
                    uri: string;
                    mimeType?: string | undefined;
                } | {
                    uri: string;
                    blob: string;
                    mimeType?: string | undefined;
                };
            })[] | undefined;
            detailedContent?: string | undefined;
        } | undefined;
        error?: {
            message: string;
            code?: string | undefined;
        } | undefined;
        toolTelemetry?: Record<string, unknown> | undefined;
        parentToolCallId?: string | undefined;
        isUserRequested?: boolean | undefined;
    };
    id: string;
    type: "tool.execution_complete";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

declare type ToolExecutionEvent = {
    kind: "tool_execution";
    turn: number;
    callId?: string;
    toolCallId: string;
    toolResult: ToolResultExpanded;
    durationMs: number;
};

export declare type ToolExecutionPartialResultEvent = z_2.infer<typeof ToolExecutionPartialResultEventSchema>;

/**
 * Tool execution partial result (streaming updates)
 * Note: These events have ephemeral: true and are NOT persisted to disk
 */
declare const ToolExecutionPartialResultEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"tool.execution_partial_result">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        partialOutput: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        partialOutput: string;
    }, {
        toolCallId: string;
        partialOutput: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        partialOutput: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_partial_result";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        toolCallId: string;
        partialOutput: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_partial_result";
    timestamp: string;
    parentId: string | null;
}>;

export declare type ToolExecutionProgressEvent = z_2.infer<typeof ToolExecutionProgressEventSchema>;

/**
 * Tool execution progress notification (e.g., from MCP servers)
 * Note: These events have ephemeral: true and are NOT persisted to disk
 */
declare const ToolExecutionProgressEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
} & {
    type: z_2.ZodLiteral<"tool.execution_progress">;
    ephemeral: z_2.ZodLiteral<true>;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        progressMessage: z_2.ZodString;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        progressMessage: string;
    }, {
        toolCallId: string;
        progressMessage: string;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        progressMessage: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_progress";
    timestamp: string;
    parentId: string | null;
}, {
    data: {
        toolCallId: string;
        progressMessage: string;
    };
    id: string;
    ephemeral: true;
    type: "tool.execution_progress";
    timestamp: string;
    parentId: string | null;
}>;

export declare type ToolExecutionStartEvent = z_2.infer<typeof ToolExecutionStartEventSchema>;

/**
 * Tool execution begins
 */
declare const ToolExecutionStartEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.execution_start">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        toolName: z_2.ZodString;
        arguments: z_2.ZodUnknown;
        mcpServerName: z_2.ZodOptional<z_2.ZodString>;
        mcpToolName: z_2.ZodOptional<z_2.ZodString>;
    } & {
        parentToolCallId: z_2.ZodOptional<z_2.ZodString>;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    }, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "tool.execution_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
        mcpServerName?: string | undefined;
        mcpToolName?: string | undefined;
        parentToolCallId?: string | undefined;
    };
    id: string;
    type: "tool.execution_start";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

/**
 * JSON Schema object for tool input parameters.
 *
 * Tool input schemas are always objects (type: "object") since function calling
 * requires named parameters. This type is compatible with:
 * - OpenAI's `FunctionParameters` (`{ [key: string]: unknown }`)
 * - Anthropic's `Tool.InputSchema` (`{ type: 'object'; [k: string]: unknown }`)
 *
 * When accessing specific schema properties (e.g., `properties`, `required`),
 * explicit type narrowing or assertions are required.
 */
declare type ToolInputSchema = {
    type: "object";
    [key: string]: unknown;
};

/**
 * An event that is emitted by the `Client` for each tool message it will send back to the LLM.
 */
declare type ToolMessageEvent = {
    kind: "message";
    turn?: number;
    callId?: string;
    modelCall?: ModelCallParam;
    message: ChatCompletionToolMessageParam;
};

/**
 * Lightweight tool metadata used for system message building, token counting, and display.
 * Contains only the properties needed for these purposes, without execution callbacks.
 *
 * This is the base type that Tool extends. Functions that only need tool metadata
 * (like cliSystemMessage) should accept ToolMetadata[] to allow passing either
 * ToolMetadata[] or Tool[] without conversion.
 */
declare type ToolMetadata = {
    /**
     * Name used to identify the tool in prompts and tool calls.
     */
    name: string;
    /**
     * Optional namespaced name for the tool used for declarative filtering of tools.
     * e.g.: "playwright/navigate"
     */
    namespacedName?: string;
    /**
     * Optional MCP metadata.
     * These are only set for MCP-backed tools and are used for telemetry/logging.
     *
     * Note: `mcpServerName` is a display/original name and may contain "/".
     */
    mcpServerName?: string;
    mcpToolName?: string;
    /** Intended for UI and end-user contexts — optimized to be human-readable
     * and easily understood, even by those unfamiliar with domain-specific terminology.
     *
     * If not provided, the name should be used for display (except for Tool, where
     * annotations.title should be given precedence over using name, if present).
     */
    title?: string;
    /**
     * Description of what the tool does.
     */
    description: string;
    /**
     * JSON Schema for the tool's input.
     * Required for function tools, optional for custom tools.
     */
    input_schema?: ToolInputSchema;
    /**
     * Optional instructions for how to use this tool effectively.
     * These instructions will be included in the system prompt's <tools> section.
     */
    instructions?: string;
};

declare type ToolProgressCallback = (callId: string, progressMessage: string) => void;

declare type ToolResult = string | ToolResultExpanded;

declare type ToolResultExpanded<TelemetryT extends Telemetry = Telemetry> = {
    /**
     * The result to be given back to the LLM.
     *
     * If @see sessionLog is omitted, then this will be used as the session log.
     */
    textResultForLlm: string;
    /**
     * The result to be given back to the LLM. It can be either base64 encoded image or audio content.
     */
    binaryResultsForLlm?: BinaryResult[];
    /**
     * Whether or not the result should be considered a success, failure, or previously interrupted.
     * - `success`: The tool executed successfully and produced a valid result.
     * - `failure`: The tool encountered an error or did not produce a valid result.
     * - `rejected`: The tool call was rejected either because the user didn't want this call, or a previous dependent one.
     * - `denied`: The tool call was denied because the permissions service said no.
     */
    resultType: "success" | "failure" | "rejected" | "denied";
    /**
     * If there was any sort of error that caused the tool to fail, then a string representation of the error. Typically
     * only set if {@link resultType} is `'failure'`.
     */
    error?: string;
    /**
     * Specific telemetry for the tool. Will be sent back to the server by the agent.
     */
    toolTelemetry?: {
        properties?: TelemetryT["properties"];
        restrictedProperties?: TelemetryT["restrictedProperties"];
        metrics?: TelemetryT["metrics"];
    };
    /**
     * Well-formatted (typically Markdown) string that can be used to display the input/output of the tool invoked.
     *
     * (Optional) If omitted, the text result for the LLM will be used as the session log.
     */
    sessionLog?: string;
    /**
     * When true, skips the large output processing that would normally write
     * large results to a temp file. Used when the caller explicitly requested
     * the full output (e.g., forceReadLargeFiles=true on the view tool).
     */
    skipLargeOutputProcessing?: boolean;
    /**
     * User messages to inject into the conversation history after this tool result.
     * These messages are added to the conversation and sent to the model, but can be
     * filtered from the timeline display based on their source.
     *
     * Use case: Skills inject their full content as user messages so the model
     * treats them as instructions to follow, while keeping the timeline clean.
     */
    newMessages?: InjectedUserMessage[];
    /**
     * Structured content blocks from tool execution.
     * Contains rich content like text, images, audio, and resources in their native format.
     * Can be populated by any tool (MCP tools, bash, etc.) that returns structured content.
     */
    contents?: ContentBlock[];
    /**
     * Information about a skill invocation. Set by the skill tool when a skill is
     * successfully loaded. The session uses this to emit a skill.invoked event
     * for tracking skills across compaction.
     * @internal
     */
    skillInvocation?: {
        /** The skill name */
        name: string;
        /** Path to the SKILL.md file */
        path: string;
        /** The full content of the skill file */
        content: string;
        /** Tools that should be auto-approved when this skill is active */
        allowedTools?: string[];
    };
};

/**
 * Hydro event types for CLI telemetry.
 * These types map to the Hydro schemas defined in github/hydro-schemas
 * under proto/hydro/schemas/copilot_cli/v0/
 *
 * See:
 * - model_call.proto
 * - tool_call.proto
 * - telemetry.proto
 * - restricted_telemetry.proto
 * - restricted_model_call.proto
 * - restricted_tool_call.proto
 * - entities/client_info.proto
 * - entities/tool_result_type.proto
 * - entities/model_choice.proto
 * - entities/model_choice_tool_call.proto
 */
/**
 * Tool execution result type.
 * Maps to: copilot_cli.v0.entities.ToolResultType
 */
declare type ToolResultType = "UNKNOWN_RESULT" | "SUCCESS" | "FAILURE";

/**
 * A callback to be called when the tool is shutting down. Gives the tool
 * a chance to clean things up, and return a telemetry event (if desired) which
 * will be emitted by the agent.
 */
declare type ToolShutdown = () => Promise<TelemetryEvent | void>;

export declare type ToolUserRequestedEvent = z_2.infer<typeof ToolUserRequestedEventSchema>;

/**
 * Tool user requested event
 */
declare const ToolUserRequestedEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"tool.user_requested">;
    data: z_2.ZodObject<{
        toolCallId: z_2.ZodString;
        toolName: z_2.ZodString;
        arguments: z_2.ZodUnknown;
    }, "strip", z_2.ZodTypeAny, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    }, {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    };
    id: string;
    type: "tool.user_requested";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        toolCallId: string;
        toolName: string;
        arguments?: unknown;
    };
    id: string;
    type: "tool.user_requested";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

/**
 * Converts full Tool objects to lightweight ToolMetadata.
 * Used to create a safe-to-cache representation without callbacks.
 */
export declare function toToolMetadata(tools: Tool_2[]): ToolMetadata[];

declare interface TransportFactory {
    createTransport(transportOptions: TransportOptions): StdioClientTransport | StreamableHTTPClientTransport | SSEClientTransport | InMemoryClientTransport;
}

declare type TransportOptions = StdioTransportConfig | HTTPTransportConfig | SSETransportConfig | InMemoryTransportConfig;

declare type TruncationEvent = {
    kind: "history_truncated";
    turn: number;
    performedBy: string;
    truncateResult: {
        tokenLimit: number;
        preTruncationTokensInMessages: number;
        preTruncationMessagesLength: number;
        postTruncationTokensInMessages: number;
        postTruncationMessagesLength: number;
        tokensRemovedDuringTruncation: number;
        messagesRemovedDuringTruncation: number;
    };
};

declare type TurnEvent = {
    kind: "turn_started" | "turn_ended" | "turn_failed" | "turn_retry";
    model: string;
    modelInfo: object;
    turn: number;
    timestampMs: number;
    error?: string;
    /** Why this retry is happening (e.g., "streaming_error", "rate_limit"). */
    reason?: string;
};

export declare type UpdatableSessionOptions = Omit<SessionOptions, "sessionId" | "startTime" | "modifiedTime" | "summary">;

/**
 * A permission request for accessing URLs.
 */
declare type UrlPermissionRequest = {
    readonly kind: "url";
    /** The intention, e.g. "Fetch web content" */
    readonly intention: string;
    /** The URL being accessed */
    readonly url: string;
};

/**
 * Event emitted every turn to report current context window token usage.
 * Unlike TruncationEvent, this is always emitted regardless of whether truncation occurred.
 */
declare type UsageInfoEvent = {
    kind: "usage_info";
    turn: number;
    tokenLimit: number;
    currentTokens: number;
    messagesLength: number;
};

/**
 * Aggregated usage metrics for a session
 */
export declare interface UsageMetrics {
    totalPremiumRequests: number;
    /** Raw count of user-initiated requests (not multiplied) */
    totalUserRequests: number;
    totalApiDurationMs: number;
    sessionStartTime: number;
    codeChanges: CodeChangeMetrics;
    modelMetrics: Map<string, ModelMetrics>;
    currentModel?: string;
}

/**
 * UsageMetricsTracker maintains usage metrics state and processes session events.
 * This is the core logic used by both the Session class and React hooks.
 */
export declare class UsageMetricsTracker {
    private _metrics;
    private fileEditingToolCallIds;
    constructor(sessionStartTime: Date);
    /**
     * Get the current usage metrics.
     * Returns a shallow copy to prevent external mutation.
     */
    get metrics(): UsageMetrics;
    /**
     * Process a session event and update metrics accordingly.
     * Call this for each event emitted by the session.
     */
    processEvent(event: SessionEvent): void;
    private processUsageEvent;
    private processAssistantMessage;
    private processToolComplete;
    /**
     * Reconstruct metrics from a full array of events.
     * Used when initializing from an existing session (e.g., resume).
     */
    static fromEvents(events: readonly SessionEvent[], sessionStartTime: Date): UsageMetricsTracker;
}

/** Represents the user-based authentication information (OAuth). */
declare type UserAuthInfo = {
    readonly type: "user";
    readonly host: string;
    readonly login: string;
    readonly copilotUser?: CopilotUserResponse;
};

export declare type UserMessageEvent = z_2.infer<typeof UserMessageEventSchema>;

/**
 * An event that is emitted by the `Client` for each user message it adds to the middle of the conversation.
 */
declare type UserMessageEvent_2 = {
    kind: "message";
    turn?: number;
    callId?: string;
    modelCall?: ModelCallParam;
    message: ChatCompletionUserMessageParam;
    /**
     * The component which was the source of the user message.
     * - `jit-instruction`: The message was injected by something which adds automated instructions for the agent
     * - `command-{id}`: The message was injected as a result of a command with the given id
     * - `string`: Some other source
     */
    source?: string;
};

declare const UserMessageEventSchema: z_2.ZodObject<{
    id: z_2.ZodString;
    timestamp: z_2.ZodString;
    parentId: z_2.ZodNullable<z_2.ZodString>;
    ephemeral: z_2.ZodOptional<z_2.ZodBoolean>;
} & {
    type: z_2.ZodLiteral<"user.message">;
    data: z_2.ZodObject<{
        content: z_2.ZodString;
        transformedContent: z_2.ZodOptional<z_2.ZodString>;
        attachments: z_2.ZodOptional<z_2.ZodArray<z_2.ZodDiscriminatedUnion<"type", [z_2.ZodObject<{
            path: z_2.ZodString;
            displayName: z_2.ZodString;
            lineRange: z_2.ZodOptional<z_2.ZodObject<{
                start: z_2.ZodNumber;
                end: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                end: number;
                start: number;
            }, {
                end: number;
                start: number;
            }>>;
        } & {
            type: z_2.ZodLiteral<"file">;
        }, "strip", z_2.ZodTypeAny, {
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }, {
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }>, z_2.ZodObject<{
            path: z_2.ZodString;
            displayName: z_2.ZodString;
            lineRange: z_2.ZodOptional<z_2.ZodObject<{
                start: z_2.ZodNumber;
                end: z_2.ZodNumber;
            }, "strip", z_2.ZodTypeAny, {
                end: number;
                start: number;
            }, {
                end: number;
                start: number;
            }>>;
        } & {
            type: z_2.ZodLiteral<"directory">;
        }, "strip", z_2.ZodTypeAny, {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }, {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        }>, z_2.ZodObject<{
            type: z_2.ZodLiteral<"selection">;
            filePath: z_2.ZodString;
            displayName: z_2.ZodString;
            text: z_2.ZodString;
            selection: z_2.ZodObject<{
                start: z_2.ZodObject<{
                    line: z_2.ZodNumber;
                    character: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    line: number;
                    character: number;
                }, {
                    line: number;
                    character: number;
                }>;
                end: z_2.ZodObject<{
                    line: z_2.ZodNumber;
                    character: z_2.ZodNumber;
                }, "strip", z_2.ZodTypeAny, {
                    line: number;
                    character: number;
                }, {
                    line: number;
                    character: number;
                }>;
            }, "strip", z_2.ZodTypeAny, {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            }, {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            }>;
        }, "strip", z_2.ZodTypeAny, {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        }, {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        }>]>, "many">>;
        source: z_2.ZodOptional<z_2.ZodString>;
        /** The agent mode active when this message was sent */
        agentMode: z_2.ZodOptional<z_2.ZodEnum<["interactive", "plan", "autopilot", "shell"]>>;
    }, "strip", z_2.ZodTypeAny, {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    }, {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    }>;
}, "strip", z_2.ZodTypeAny, {
    data: {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    };
    id: string;
    type: "user.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}, {
    data: {
        content: string;
        source?: string | undefined;
        transformedContent?: string | undefined;
        attachments?: ({
            type: "file";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "directory";
            path: string;
            displayName: string;
            lineRange?: {
                end: number;
                start: number;
            } | undefined;
        } | {
            type: "selection";
            text: string;
            filePath: string;
            displayName: string;
            selection: {
                end: {
                    line: number;
                    character: number;
                };
                start: {
                    line: number;
                    character: number;
                };
            };
        })[] | undefined;
        agentMode?: "shell" | "interactive" | "plan" | "autopilot" | undefined;
    };
    id: string;
    type: "user.message";
    timestamp: string;
    parentId: string | null;
    ephemeral?: boolean | undefined;
}>;

export declare type UserPromptSubmittedHook = (input: UserPromptSubmittedHookInput) => Promise<UserPromptSubmittedHookOutput | void>;

/**
 * User prompt submitted hook types
 */
export declare interface UserPromptSubmittedHookInput extends BaseHookInput {
    prompt: string;
}

export declare interface UserPromptSubmittedHookOutput {
    modifiedPrompt?: string;
    additionalContext?: string;
    suppressOutput?: boolean;
}

declare type WildcardEventHandler = (event: SessionEvent) => void;

/**
 * Working directory context for session tracking
 */
declare interface WorkingDirectoryContext {
    /** Current working directory */
    cwd: string;
    /** Git repository root, if in a git repo */
    gitRoot?: string;
    /** GitHub repository in "owner/repo" format, if available */
    repository?: string;
    /** Current git branch, if in a git repo */
    branch?: string;
}

declare type Workspace = z.infer<typeof WorkspaceSchema>;

/**
 * Log message prefix emitted when a session workspace is initialized.
 * This marker is used by `findSessionLogFiles` to identify which process log
 * files belong to a given session, so changes here must stay in sync.
 */
export declare const WORKSPACE_INITIALIZED_LOG_PREFIX = "Workspace initialized:";

/**
 * Working directory context for workspace creation/update
 */
declare interface WorkspaceContext {
    cwd?: string;
    gitRoot?: string;
    repository?: string;
    branch?: string;
}

/**
 * Information about the current workspace for context injection into prompts.
 */
declare interface WorkspaceContextInfo {
    /** Name of the workspace (for display) */
    name?: string;
    /** Path to the workspace directory */
    workspacePath: string;
    /** Number of prior session summaries */
    summaryCount: number;
    /** Whether a plan.md file exists */
    hasPlan: boolean;
    /** List of files in the files/ directory (if any) */
    filesInWorkspace?: string[];
    /** List of checkpoints with their titles */
    checkpoints?: CheckpointInfo[];
}

/**
 * Schema for workspace metadata.
 * Stored at: ~/.copilot/session-state/{session-id}/workspace.yaml
 */
declare const WorkspaceSchema: z.ZodObject<{
    id: z.ZodString;
    cwd: z.ZodOptional<z.ZodString>;
    git_root: z.ZodOptional<z.ZodString>;
    repository: z.ZodOptional<z.ZodString>;
    branch: z.ZodOptional<z.ZodString>;
    summary: z.ZodOptional<z.ZodString>;
    name: z.ZodOptional<z.ZodString>;
    summary_count: z.ZodDefault<z.ZodNumber>;
    created_at: z.ZodOptional<z.ZodString>;
    updated_at: z.ZodOptional<z.ZodString>;
}, "strip", z.ZodTypeAny, {
    id: string;
    summary_count: number;
    name?: string | undefined;
    branch?: string | undefined;
    summary?: string | undefined;
    cwd?: string | undefined;
    repository?: string | undefined;
    git_root?: string | undefined;
    created_at?: string | undefined;
    updated_at?: string | undefined;
}, {
    id: string;
    name?: string | undefined;
    branch?: string | undefined;
    summary?: string | undefined;
    cwd?: string | undefined;
    repository?: string | undefined;
    git_root?: string | undefined;
    summary_count?: number | undefined;
    created_at?: string | undefined;
    updated_at?: string | undefined;
}>;

/**
 * A permission request for writing to new or existing files.
 */
declare type WritePermissionRequest = {
    readonly kind: "write";
    /** The intention of the edit operation, e.g. "Edit file" or "Create file" */
    readonly intention: string;
    /** The name of the file being edited */
    readonly fileName: string;
    /** The diff of the changes being made */
    readonly diff: string;
    /** The new file contents (for IDE diff view) */
    readonly newFileContents?: string;
};

export { }
