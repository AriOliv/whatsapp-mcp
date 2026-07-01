/**
 * Declarative tool catalog for the Evolution API.
 *
 * Each entry maps an MCP tool to one HTTP endpoint. Instance-scoped tools use a
 * `{instance}` segment (usesInstance:true) auto-filled from the active instance
 * name. Routes mirror src/api/routes/*.router.ts of EvolutionAPI/evolution-api:
 *   instance -> /instance, send -> /message, chat -> /chat, group -> /group,
 *   call -> /call, label -> /label, settings -> /settings.
 *
 * Body shapes follow the DTOs (src/api/dto). Nested objects/arrays (where, key,
 * buttons, sections, contact, numbers, …) are passed through to the JSON body
 * verbatim — declare them as object/array in the schema.
 */
import type { McpToolDefinition } from "./types.js";

// Reusable schema snippets ---------------------------------------------------
const numberProp = {
  number: {
    type: "string",
    description: "Recipient phone in international format without +, e.g. 5521999999999 (or a group JID).",
  },
};
const sendMeta = {
  delay: { type: "number", description: "Delay in ms before sending (optional)." },
  quoted: { type: "object", description: "Quote a message: { key: { id }, message }. Optional." },
  linkPreview: { type: "boolean", description: "Enable link preview (optional)." },
  mentionsEveryOne: { type: "boolean", description: "Mention everyone in a group (optional)." },
  mentioned: { type: "array", items: { type: "string" }, description: "JIDs to mention (optional)." },
};
const queryFilter = {
  where: {
    type: "object",
    description:
      "Prisma-style filter. For messages, target a chat with { \"key\": { \"remoteJid\": \"5521XXXX@s.whatsapp.net\" } }. For contacts/chats use { \"remoteJid\": \"...\" }. Omit to list all.",
  },
  page: { type: "number", description: "Page number (optional)." },
  offset: { type: "number", description: "Page size / offset (optional)." },
};

export const toolDefinitions: McpToolDefinition[] = [
  // ===================== INSTANCE (admin) =====================
  {
    name: "evo_instance_create",
    description:
      "Create a new WhatsApp instance (Baileys). On success the new instance becomes the active one. Then connect it with evo_instance_connect and scan the QR.",
    inputSchema: {
      properties: {
        instanceName: { type: "string", description: "Unique instance name." },
        integration: {
          type: "string",
          description: "Integration type. Default WHATSAPP-BAILEYS.",
          enum: ["WHATSAPP-BAILEYS", "WHATSAPP-BUSINESS"],
        },
        qrcode: { type: "boolean", description: "Return a QR code immediately. Default true." },
        number: { type: "string", description: "Optional phone number for pairing-code flow." },
      },
      required: ["instanceName"],
    },
    method: "post",
    pathTemplate: "/instance/create",
    bodyParams: ["instanceName", "integration", "qrcode", "number"],
    requiresAuth: true,
  },
  {
    name: "evo_instance_list",
    description:
      "List instances (fetchInstances). Optionally filter by instanceName or instanceId. Includes connection status and counts.",
    inputSchema: {
      properties: {
        instanceName: { type: "string", description: "Filter by name (optional)." },
        instanceId: { type: "string", description: "Filter by id (optional)." },
      },
    },
    method: "get",
    pathTemplate: "/instance/fetchInstances",
    queryParams: ["instanceName", "instanceId"],
    requiresAuth: true,
  },
  {
    name: "evo_instance_connect",
    description: "Start/refresh the connection for an instance and return a QR code (base64) / pairing code.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance name (defaults to active)." },
        number: { type: "string", description: "Phone for pairing-code flow (optional)." },
      },
    },
    method: "get",
    pathTemplate: "/instance/connect/{instance}",
    usesInstance: true,
    queryParams: ["number"],
    requiresAuth: true,
  },
  {
    name: "evo_instance_state",
    description: "Get the connection state of an instance (open | connecting | close).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance name (defaults to active)." } } },
    method: "get",
    pathTemplate: "/instance/connectionState/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_instance_restart",
    description: "Restart an instance's socket (useful to get a fresh QR).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance name (defaults to active)." } } },
    method: "post",
    pathTemplate: "/instance/restart/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_instance_logout",
    description: "Log out (unlink) the WhatsApp device for an instance. Keeps the instance; requires re-scanning a QR to use again.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance name (defaults to active)." } } },
    method: "delete",
    pathTemplate: "/instance/logout/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_instance_delete",
    description: "Delete an instance entirely (must be logged out first).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance name (defaults to active)." } } },
    method: "delete",
    pathTemplate: "/instance/delete/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_set_presence",
    description: "Set the instance's global presence (available | unavailable).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance name (defaults to active)." },
        presence: { type: "string", enum: ["available", "unavailable"], description: "Presence value." },
      },
      required: ["presence"],
    },
    method: "post",
    pathTemplate: "/instance/setPresence/{instance}",
    usesInstance: true,
    bodyParams: ["presence"],
    requiresAuth: true,
  },

  // ===================== READ: chats / messages / contacts =====================
  {
    name: "evo_find_chats",
    description:
      "List the instance's chats (conversations) from the database. Optionally filter via `where`. Use this to see your conversations.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...queryFilter } },
    method: "post",
    pathTemplate: "/chat/findChats/{instance}",
    usesInstance: true,
    bodyParams: ["where", "page", "offset"],
    requiresAuth: true,
  },
  {
    name: "evo_find_messages",
    description:
      "Fetch stored messages, optionally for one contact/chat. To read a conversation with a contact, pass where = { \"key\": { \"remoteJid\": \"5521XXXXXXXXX@s.whatsapp.net\" } }. Supports page.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...queryFilter } },
    method: "post",
    pathTemplate: "/chat/findMessages/{instance}",
    usesInstance: true,
    bodyParams: ["where", "page", "offset"],
    requiresAuth: true,
  },
  {
    name: "evo_find_contacts",
    description: "List stored contacts. Optionally filter via `where` (e.g. { \"remoteJid\": \"...@s.whatsapp.net\" }).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...queryFilter } },
    method: "post",
    pathTemplate: "/chat/findContacts/{instance}",
    usesInstance: true,
    bodyParams: ["where", "page", "offset"],
    requiresAuth: true,
  },
  {
    name: "evo_find_status_messages",
    description: "Fetch status (read receipts / message update) records. Optionally filter via `where`.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...queryFilter } },
    method: "post",
    pathTemplate: "/chat/findStatusMessage/{instance}",
    usesInstance: true,
    bodyParams: ["where", "page", "offset"],
    requiresAuth: true,
  },
  {
    name: "evo_find_chat_by_jid",
    description: "Fetch a single chat by its remoteJid.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        remoteJid: { type: "string", description: "Chat JID, e.g. 5521XXXX@s.whatsapp.net." },
      },
      required: ["remoteJid"],
    },
    method: "get",
    pathTemplate: "/chat/findChatByRemoteJid/{instance}",
    usesInstance: true,
    queryParams: ["remoteJid"],
    requiresAuth: true,
  },
  {
    name: "evo_check_numbers",
    description: "Check whether phone numbers are registered on WhatsApp.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        numbers: { type: "array", items: { type: "string" }, description: "Phone numbers to check." },
      },
      required: ["numbers"],
    },
    method: "post",
    pathTemplate: "/chat/whatsappNumbers/{instance}",
    usesInstance: true,
    bodyParams: ["numbers"],
    requiresAuth: true,
  },
  {
    name: "evo_get_media_base64",
    description: "Download the media of a message and return it as base64.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        message: { type: "object", description: "The message object (or { key: {...} }) to download media from." },
        convertToMp4: { type: "boolean", description: "Convert audio/video to mp4 (optional)." },
      },
      required: ["message"],
    },
    method: "post",
    pathTemplate: "/chat/getBase64FromMediaMessage/{instance}",
    usesInstance: true,
    bodyParams: ["message", "convertToMp4"],
    requiresAuth: true,
  },
  {
    name: "evo_profile_picture_url",
    description: "Get a contact's profile picture URL.",
    inputSchema: {
      properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...numberProp },
      required: ["number"],
    },
    method: "post",
    pathTemplate: "/chat/fetchProfilePictureUrl/{instance}",
    usesInstance: true,
    bodyParams: ["number"],
    requiresAuth: true,
  },

  // ===================== CHAT actions =====================
  {
    name: "evo_mark_read",
    description: "Mark messages as read.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        readMessages: {
          type: "array",
          description: "Array of message keys: [{ remoteJid, fromMe, id }].",
          items: { type: "object" },
        },
      },
      required: ["readMessages"],
    },
    method: "post",
    pathTemplate: "/chat/markMessageAsRead/{instance}",
    usesInstance: true,
    bodyParams: ["readMessages"],
    requiresAuth: true,
  },
  {
    name: "evo_mark_unread",
    description: "Mark a chat as unread.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        lastMessage: { type: "object", description: "The last message { key: {...} } of the chat." },
        chat: { type: "string", description: "Chat JID (alternative to lastMessage)." },
      },
    },
    method: "post",
    pathTemplate: "/chat/markChatUnread/{instance}",
    usesInstance: true,
    bodyParams: ["lastMessage", "chat"],
    requiresAuth: true,
  },
  {
    name: "evo_archive_chat",
    description: "Archive or unarchive a chat.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        chat: { type: "string", description: "Chat JID." },
        lastMessage: { type: "object", description: "Last message { key: {...} } (optional)." },
        archive: { type: "boolean", description: "true to archive, false to unarchive." },
      },
      required: ["archive"],
    },
    method: "post",
    pathTemplate: "/chat/archiveChat/{instance}",
    usesInstance: true,
    bodyParams: ["chat", "lastMessage", "archive"],
    requiresAuth: true,
  },
  {
    name: "evo_delete_message",
    description: "Delete a message for everyone.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        id: { type: "string", description: "Message id." },
        remoteJid: { type: "string", description: "Chat JID." },
        fromMe: { type: "boolean", description: "Whether the message is from me." },
        participant: { type: "string", description: "Participant JID (for groups, optional)." },
      },
      required: ["id", "remoteJid", "fromMe"],
    },
    method: "delete",
    pathTemplate: "/chat/deleteMessageForEveryone/{instance}",
    usesInstance: true,
    bodyParams: ["id", "remoteJid", "fromMe", "participant"],
    requiresAuth: true,
  },
  {
    name: "evo_update_message",
    description: "Edit a previously sent text message.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        number: { type: "string", description: "Recipient number/JID." },
        text: { type: "string", description: "New text." },
        key: { type: "object", description: "The original message key { remoteJid, fromMe, id }." },
      },
      required: ["number", "text", "key"],
    },
    method: "post",
    pathTemplate: "/chat/updateMessage/{instance}",
    usesInstance: true,
    bodyParams: ["number", "text", "key"],
    requiresAuth: true,
  },
  {
    name: "evo_send_chat_presence",
    description: "Send a typing/recording presence to a chat.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        presence: { type: "string", enum: ["composing", "recording", "paused"], description: "Presence type." },
        delay: { type: "number", description: "Duration in ms." },
      },
      required: ["number", "presence"],
    },
    method: "post",
    pathTemplate: "/chat/sendPresence/{instance}",
    usesInstance: true,
    bodyParams: ["number", "presence", "delay"],
    requiresAuth: true,
  },
  {
    name: "evo_block_contact",
    description: "Block or unblock a contact.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        number: { type: "string", description: "Contact number/JID." },
        status: { type: "string", enum: ["block", "unblock"], description: "Action." },
      },
      required: ["number", "status"],
    },
    method: "post",
    pathTemplate: "/chat/updateBlockStatus/{instance}",
    usesInstance: true,
    bodyParams: ["number", "status"],
    requiresAuth: true,
  },

  // ===================== SEND =====================
  {
    name: "evo_send_text",
    description: "Send a text message.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        text: { type: "string", description: "Message text." },
        ...sendMeta,
      },
      required: ["number", "text"],
    },
    method: "post",
    pathTemplate: "/message/sendText/{instance}",
    usesInstance: true,
    bodyParams: ["number", "text", "delay", "quoted", "linkPreview", "mentionsEveryOne", "mentioned"],
    requiresAuth: true,
  },
  {
    name: "evo_send_media",
    description: "Send media (image, video or document) by URL or base64.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        mediatype: { type: "string", enum: ["image", "video", "document"], description: "Media type." },
        media: { type: "string", description: "Media URL or base64 string." },
        mimetype: { type: "string", description: "MIME type (optional)." },
        caption: { type: "string", description: "Caption (optional)." },
        fileName: { type: "string", description: "File name (recommended for documents)." },
        ...sendMeta,
      },
      required: ["number", "mediatype", "media"],
    },
    method: "post",
    pathTemplate: "/message/sendMedia/{instance}",
    usesInstance: true,
    bodyParams: ["number", "mediatype", "media", "mimetype", "caption", "fileName", "delay", "quoted", "mentionsEveryOne", "mentioned"],
    requiresAuth: true,
  },
  {
    name: "evo_send_audio",
    description: "Send a WhatsApp voice/audio message (URL or base64).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        audio: { type: "string", description: "Audio URL or base64." },
        ...sendMeta,
      },
      required: ["number", "audio"],
    },
    method: "post",
    pathTemplate: "/message/sendWhatsAppAudio/{instance}",
    usesInstance: true,
    bodyParams: ["number", "audio", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_sticker",
    description: "Send a sticker (URL or base64).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        sticker: { type: "string", description: "Sticker URL or base64." },
        ...sendMeta,
      },
      required: ["number", "sticker"],
    },
    method: "post",
    pathTemplate: "/message/sendSticker/{instance}",
    usesInstance: true,
    bodyParams: ["number", "sticker", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_location",
    description: "Send a location.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        latitude: { type: "number", description: "Latitude." },
        longitude: { type: "number", description: "Longitude." },
        name: { type: "string", description: "Place name (optional)." },
        address: { type: "string", description: "Address (optional)." },
        ...sendMeta,
      },
      required: ["number", "latitude", "longitude"],
    },
    method: "post",
    pathTemplate: "/message/sendLocation/{instance}",
    usesInstance: true,
    bodyParams: ["number", "latitude", "longitude", "name", "address", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_contact",
    description: "Send one or more contacts.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        contact: {
          type: "array",
          description: "Array of contacts: [{ fullName, wuid, phoneNumber, organization?, email?, url? }].",
          items: { type: "object" },
        },
      },
      required: ["number", "contact"],
    },
    method: "post",
    pathTemplate: "/message/sendContact/{instance}",
    usesInstance: true,
    bodyParams: ["number", "contact"],
    requiresAuth: true,
  },
  {
    name: "evo_send_reaction",
    description: "React to a message with an emoji.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        key: { type: "object", description: "Target message key { remoteJid, fromMe, id }." },
        reaction: { type: "string", description: "Emoji, e.g. 👍 (empty string removes the reaction)." },
      },
      required: ["key", "reaction"],
    },
    method: "post",
    pathTemplate: "/message/sendReaction/{instance}",
    usesInstance: true,
    bodyParams: ["key", "reaction"],
    requiresAuth: true,
  },
  {
    name: "evo_send_poll",
    description: "Send a poll.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        name: { type: "string", description: "Poll question." },
        selectableCount: { type: "number", description: "How many options a voter can pick." },
        values: { type: "array", items: { type: "string" }, description: "Poll options." },
        ...sendMeta,
      },
      required: ["number", "name", "selectableCount", "values"],
    },
    method: "post",
    pathTemplate: "/message/sendPoll/{instance}",
    usesInstance: true,
    bodyParams: ["number", "name", "selectableCount", "values", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_list",
    description: "Send an interactive list message. NOTE: may not render for non-official senders on many WhatsApp clients.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        title: { type: "string", description: "List title." },
        description: { type: "string", description: "List description (optional)." },
        buttonText: { type: "string", description: "Text on the open-list button." },
        footerText: { type: "string", description: "Footer (optional)." },
        sections: { type: "array", items: { type: "object" }, description: "Sections: [{ title, rows: [{ title, description, rowId }] }]." },
        ...sendMeta,
      },
      required: ["number", "title", "buttonText", "sections"],
    },
    method: "post",
    pathTemplate: "/message/sendList/{instance}",
    usesInstance: true,
    bodyParams: ["number", "title", "description", "buttonText", "footerText", "sections", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_buttons",
    description: "Send a buttons message. NOTE: may not render for non-official senders on many WhatsApp clients.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        ...numberProp,
        title: { type: "string", description: "Title." },
        description: { type: "string", description: "Description (optional)." },
        footer: { type: "string", description: "Footer (optional)." },
        buttons: { type: "array", items: { type: "object" }, description: "Buttons array (see Evolution API docs for the shape)." },
        ...sendMeta,
      },
      required: ["number", "title", "buttons"],
    },
    method: "post",
    pathTemplate: "/message/sendButtons/{instance}",
    usesInstance: true,
    bodyParams: ["number", "title", "description", "footer", "buttons", "delay", "quoted"],
    requiresAuth: true,
  },
  {
    name: "evo_send_status",
    description: "Post a WhatsApp status (story).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        type: { type: "string", enum: ["text", "image", "video", "audio"], description: "Status type." },
        content: { type: "string", description: "Text content or media URL/base64." },
        caption: { type: "string", description: "Caption (media, optional)." },
        backgroundColor: { type: "string", description: "Background color (text, optional)." },
        font: { type: "number", description: "Font index (text, optional)." },
        allContacts: { type: "boolean", description: "Send to all contacts (optional)." },
        statusJidList: { type: "array", items: { type: "string" }, description: "Specific recipient JIDs (optional)." },
      },
      required: ["type", "content"],
    },
    method: "post",
    pathTemplate: "/message/sendStatus/{instance}",
    usesInstance: true,
    bodyParams: ["type", "content", "caption", "backgroundColor", "font", "allContacts", "statusJidList"],
    requiresAuth: true,
  },

  // ===================== PROFILE =====================
  {
    name: "evo_fetch_business_profile",
    description: "Fetch a number's WhatsApp Business profile.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...numberProp }, required: ["number"] },
    method: "post",
    pathTemplate: "/chat/fetchBusinessProfile/{instance}",
    usesInstance: true,
    bodyParams: ["number"],
    requiresAuth: true,
  },
  {
    name: "evo_fetch_profile",
    description: "Fetch a number's profile (name, status, picture).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, ...numberProp }, required: ["number"] },
    method: "post",
    pathTemplate: "/chat/fetchProfile/{instance}",
    usesInstance: true,
    bodyParams: ["number"],
    requiresAuth: true,
  },
  {
    name: "evo_update_profile_name",
    description: "Update the instance's own profile name.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, name: { type: "string", description: "New profile name." } }, required: ["name"] },
    method: "post",
    pathTemplate: "/chat/updateProfileName/{instance}",
    usesInstance: true,
    bodyParams: ["name"],
    requiresAuth: true,
  },
  {
    name: "evo_update_profile_status",
    description: "Update the instance's own profile status (about).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, status: { type: "string", description: "New status text." } }, required: ["status"] },
    method: "post",
    pathTemplate: "/chat/updateProfileStatus/{instance}",
    usesInstance: true,
    bodyParams: ["status"],
    requiresAuth: true,
  },
  {
    name: "evo_update_profile_picture",
    description: "Update the instance's own profile picture (URL or base64).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." }, picture: { type: "string", description: "Image URL or base64." } }, required: ["picture"] },
    method: "post",
    pathTemplate: "/chat/updateProfilePicture/{instance}",
    usesInstance: true,
    bodyParams: ["picture"],
    requiresAuth: true,
  },
  {
    name: "evo_remove_profile_picture",
    description: "Remove the instance's own profile picture.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." } } },
    method: "delete",
    pathTemplate: "/chat/removeProfilePicture/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_get_privacy",
    description: "Fetch the instance's privacy settings.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." } } },
    method: "get",
    pathTemplate: "/chat/fetchPrivacySettings/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_update_privacy",
    description: "Update privacy settings (readreceipts, profile, status, online, last, groupadd).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        readreceipts: { type: "string", description: "all | none (optional)." },
        profile: { type: "string", description: "all | contacts | contact_blacklist | none (optional)." },
        status: { type: "string", description: "all | contacts | contact_blacklist | none (optional)." },
        online: { type: "string", description: "all | match_last_seen (optional)." },
        last: { type: "string", description: "all | contacts | contact_blacklist | none (optional)." },
        groupadd: { type: "string", description: "all | contacts | contact_blacklist | none (optional)." },
      },
    },
    method: "post",
    pathTemplate: "/chat/updatePrivacySettings/{instance}",
    usesInstance: true,
    bodyParams: ["readreceipts", "profile", "status", "online", "last", "groupadd"],
    requiresAuth: true,
  },

  // ===================== GROUP =====================
  {
    name: "evo_group_create",
    description: "Create a group.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        subject: { type: "string", description: "Group name." },
        description: { type: "string", description: "Group description (optional)." },
        participants: { type: "array", items: { type: "string" }, description: "Member phone numbers/JIDs." },
      },
      required: ["subject", "participants"],
    },
    method: "post",
    pathTemplate: "/group/create/{instance}",
    usesInstance: true,
    bodyParams: ["subject", "description", "participants"],
    requiresAuth: true,
  },
  {
    name: "evo_group_fetch_all",
    description: "Fetch all groups the instance belongs to.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        getParticipants: { type: "boolean", description: "Include participants. Default false." },
      },
    },
    method: "get",
    pathTemplate: "/group/fetchAllGroups/{instance}",
    usesInstance: true,
    queryParams: ["getParticipants"],
    requiresAuth: true,
  },
  {
    name: "evo_group_info",
    description: "Fetch info for one group by its JID.",
    inputSchema: {
      properties: { instance: { type: "string", description: "Instance (defaults to active)." }, groupJid: { type: "string", description: "Group JID." } },
      required: ["groupJid"],
    },
    method: "get",
    pathTemplate: "/group/findGroupInfos/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    requiresAuth: true,
  },
  {
    name: "evo_group_participants",
    description: "List a group's participants.",
    inputSchema: {
      properties: { instance: { type: "string", description: "Instance (defaults to active)." }, groupJid: { type: "string", description: "Group JID." } },
      required: ["groupJid"],
    },
    method: "get",
    pathTemplate: "/group/participants/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    requiresAuth: true,
  },
  {
    name: "evo_group_invite_code",
    description: "Get a group's invite code/link.",
    inputSchema: {
      properties: { instance: { type: "string", description: "Instance (defaults to active)." }, groupJid: { type: "string", description: "Group JID." } },
      required: ["groupJid"],
    },
    method: "get",
    pathTemplate: "/group/inviteCode/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    requiresAuth: true,
  },
  {
    name: "evo_group_update_participant",
    description: "Add/remove/promote/demote group participants.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        groupJid: { type: "string", description: "Group JID." },
        action: { type: "string", enum: ["add", "remove", "promote", "demote"], description: "Action." },
        participants: { type: "array", items: { type: "string" }, description: "Target JIDs/numbers." },
      },
      required: ["groupJid", "action", "participants"],
    },
    method: "post",
    pathTemplate: "/group/updateParticipant/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    bodyParams: ["action", "participants"],
    requiresAuth: true,
  },
  {
    name: "evo_group_update_subject",
    description: "Change a group's name/subject.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        groupJid: { type: "string", description: "Group JID." },
        subject: { type: "string", description: "New subject." },
      },
      required: ["groupJid", "subject"],
    },
    method: "post",
    pathTemplate: "/group/updateGroupSubject/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    bodyParams: ["subject"],
    requiresAuth: true,
  },
  {
    name: "evo_group_leave",
    description: "Leave a group.",
    inputSchema: {
      properties: { instance: { type: "string", description: "Instance (defaults to active)." }, groupJid: { type: "string", description: "Group JID." } },
      required: ["groupJid"],
    },
    method: "delete",
    pathTemplate: "/group/leaveGroup/{instance}",
    usesInstance: true,
    queryParams: ["groupJid"],
    requiresAuth: true,
  },

  // ===================== LABEL =====================
  {
    name: "evo_label_find",
    description: "List labels (WhatsApp Business).",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." } } },
    method: "get",
    pathTemplate: "/label/findLabels/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },
  {
    name: "evo_label_handle",
    description: "Add or remove a label on a chat.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        number: { type: "string", description: "Chat number/JID." },
        labelId: { type: "string", description: "Label id." },
        action: { type: "string", enum: ["add", "remove"], description: "Action." },
      },
      required: ["number", "labelId", "action"],
    },
    method: "post",
    pathTemplate: "/label/handleLabel/{instance}",
    usesInstance: true,
    bodyParams: ["number", "labelId", "action"],
    requiresAuth: true,
  },

  // ===================== SETTINGS =====================
  {
    name: "evo_settings_set",
    description: "Set instance behavior settings (rejectCall, msgCall, groupsIgnore, alwaysOnline, readMessages, readStatus, syncFullHistory).",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        rejectCall: { type: "boolean", description: "Auto-reject calls." },
        msgCall: { type: "string", description: "Message sent on auto-rejected call." },
        groupsIgnore: { type: "boolean", description: "Ignore group messages." },
        alwaysOnline: { type: "boolean", description: "Keep presence always online." },
        readMessages: { type: "boolean", description: "Auto-mark messages read." },
        readStatus: { type: "boolean", description: "Auto-read statuses." },
        syncFullHistory: { type: "boolean", description: "Sync full history on connect." },
      },
    },
    method: "post",
    pathTemplate: "/settings/set/{instance}",
    usesInstance: true,
    bodyParams: ["rejectCall", "msgCall", "groupsIgnore", "alwaysOnline", "readMessages", "readStatus", "syncFullHistory"],
    requiresAuth: true,
  },
  {
    name: "evo_settings_find",
    description: "Get the instance's current settings.",
    inputSchema: { properties: { instance: { type: "string", description: "Instance (defaults to active)." } } },
    method: "get",
    pathTemplate: "/settings/find/{instance}",
    usesInstance: true,
    requiresAuth: true,
  },

  // ===================== CALL =====================
  {
    name: "evo_call_offer",
    description: "Send a (fake) call offer to a number.",
    inputSchema: {
      properties: {
        instance: { type: "string", description: "Instance (defaults to active)." },
        number: { type: "string", description: "Target number/JID." },
        isVideo: { type: "boolean", description: "Video call. Default false." },
        callDuration: { type: "number", description: "Duration in seconds." },
      },
      required: ["number"],
    },
    method: "post",
    pathTemplate: "/call/offer/{instance}",
    usesInstance: true,
    bodyParams: ["number", "isVideo", "callDuration"],
    requiresAuth: true,
  },
];

export const toolMap: Map<string, McpToolDefinition> = new Map(toolDefinitions.map((d) => [d.name, d]));

/**
 * Tools hidden on the remote (HTTP/multi-tenant) transport. Instance lifecycle is
 * owned by the pairing login page there, so exposing these would either let a
 * tenant nuke/recreate their instance out-of-band or, worse, enumerate other
 * tenants' instances (`evo_instance_list`). They remain available over stdio,
 * where the server is single-user.
 */
export const HIDDEN_IN_HTTP: ReadonlySet<string> = new Set([
  "evo_instance_create",
  "evo_instance_list",
  "evo_instance_connect",
  "evo_instance_state",
  "evo_instance_restart",
  "evo_instance_logout",
  "evo_instance_delete",
]);
