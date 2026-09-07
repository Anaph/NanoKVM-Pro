import { useEffect, useState } from 'react';
import { Button, Input, message, Select, Tooltip } from 'antd';
import { CircleAlertIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

type Preset = {
  name: string;
  description: string;
};

type Identity = {
  vendorId: string;
  productId: string;
  manufacturer: string;
  product: string;
  serial: string;
};

const CUSTOM_PRESET = 'custom';

const emptyIdentity: Identity = {
  vendorId: '',
  productId: '',
  manufacturer: '',
  product: '',
  serial: ''
};

export const UsbIdentity = () => {
  const { t } = useTranslation();
  const [messageApi, contextHolder] = message.useMessage();

  const [identity, setIdentity] = useState<Identity>(emptyIdentity);
  const [preset, setPreset] = useState(CUSTOM_PRESET);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    setIsLoading(true);

    api
      .getUsbIdentity()
      .then((rsp) => {
        if (rsp.code !== 0 || !rsp.data) {
          console.log(rsp.msg);
          return;
        }
        applyResponse(rsp.data);
      })
      .finally(() => {
        setIsLoading(false);
      });
  }, []);

  function applyResponse(data: Identity & { preset: string; presets: Preset[] }) {
    setIdentity({
      vendorId: data.vendorId,
      productId: data.productId,
      manufacturer: data.manufacturer,
      product: data.product,
      serial: data.serial
    });
    setPreset(data.preset);
    setPresets(data.presets ?? []);
  }

  // Rewriting the descriptor detaches the gadget from the USB controller, so the
  // attached host sees the keyboard and mouse disconnect and come back.
  async function submit(payload: Parameters<typeof api.setUsbIdentity>[0]) {
    if (isLoading) return;
    setIsLoading(true);

    try {
      const rsp = await api.setUsbIdentity(payload);
      if (rsp.code !== 0) {
        messageApi.error(rsp.msg || t('settings.device.usbIdentity.failed'));
        return;
      }

      applyResponse(rsp.data);
      messageApi.success(t('settings.device.usbIdentity.applied'));
    } finally {
      setIsLoading(false);
    }
  }

  function presetOptions() {
    const options = presets.map((item) => ({
      value: item.name,
      label: t(`settings.device.usbIdentity.presets.${item.name}`, item.description)
    }));

    if (!presets.some((item) => item.name === CUSTOM_PRESET)) {
      options.push({
        value: CUSTOM_PRESET,
        label: t('settings.device.usbIdentity.presets.custom')
      });
    }

    return options;
  }

  function updateField(field: keyof Identity, value: string) {
    setIdentity({ ...identity, [field]: value });
    setPreset(CUSTOM_PRESET);
  }

  return (
    <div className="flex flex-col space-y-4">
      {contextHolder}

      <div className="flex items-center justify-between space-x-5">
        <div className="flex flex-col space-y-1">
          <div className="flex items-center space-x-2">
            <span>{t('settings.device.usbIdentity.title')}</span>

            <Tooltip
              title={t('settings.device.usbIdentity.tip')}
              className="cursor-pointer text-neutral-500"
              placement="top"
              styles={{ root: { maxWidth: '400px' } }}
            >
              <CircleAlertIcon size={15} />
            </Tooltip>
          </div>

          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.description')}
          </span>
        </div>

        <Select
          value={preset}
          style={{ width: 240 }}
          loading={isLoading}
          options={presetOptions()}
          onSelect={(value) => {
            if (value === CUSTOM_PRESET) {
              setPreset(CUSTOM_PRESET);
              return;
            }
            void submit({ preset: value });
          }}
        />
      </div>

      <div className="flex flex-col space-y-3 pl-1">
        <div className="flex items-center justify-between space-x-5">
          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.vendorId')}
          </span>
          <Input
            disabled={isLoading}
            style={{ width: 240 }}
            value={identity.vendorId}
            placeholder="0x046d"
            onChange={(e) => updateField('vendorId', e.target.value)}
          />
        </div>

        <div className="flex items-center justify-between space-x-5">
          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.productId')}
          </span>
          <Input
            disabled={isLoading}
            style={{ width: 240 }}
            value={identity.productId}
            placeholder="0xc517"
            onChange={(e) => updateField('productId', e.target.value)}
          />
        </div>

        <div className="flex items-center justify-between space-x-5">
          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.manufacturer')}
          </span>
          <Input
            disabled={isLoading}
            style={{ width: 240 }}
            value={identity.manufacturer}
            placeholder="Logitech"
            onChange={(e) => updateField('manufacturer', e.target.value)}
          />
        </div>

        <div className="flex items-center justify-between space-x-5">
          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.product')}
          </span>
          <Input
            disabled={isLoading}
            style={{ width: 240 }}
            value={identity.product}
            placeholder="USB Receiver"
            onChange={(e) => updateField('product', e.target.value)}
          />
        </div>

        <div className="flex items-center justify-between space-x-5">
          <span className="text-xs text-neutral-500">
            {t('settings.device.usbIdentity.serial')}
          </span>
          <Input
            disabled={isLoading}
            style={{ width: 240 }}
            value={identity.serial}
            onChange={(e) => updateField('serial', e.target.value)}
          />
        </div>

        <div className="flex justify-end">
          <Button
            type="primary"
            loading={isLoading}
            onClick={() =>
              void submit({
                vendorId: identity.vendorId,
                productId: identity.productId,
                manufacturer: identity.manufacturer,
                product: identity.product,
                serial: identity.serial
              })
            }
          >
            {t('settings.device.usbIdentity.apply')}
          </Button>
        </div>
      </div>
    </div>
  );
};
